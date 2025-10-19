package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	gvntypes "github.com/containers/gvisor-tap-vsock/pkg/types"
	gvnvirtualnetwork "github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"golang.org/x/net/websocket"
)

const (
	gatewayIP = "192.168.127.1"
	vmIP      = "192.168.127.3"
	vmMAC     = "02:00:00:00:00:01"
)

func main() {
	var portFlags sliceFlags
	flag.Var(&portFlags, "p", "map port between host and guest (host:guest). -mac must be set correctly.")
	var (
		debug         = flag.Bool("debug", false, "enable debug print")
		listenWS      = flag.Bool("listen-ws", false, "listen on a websocket port specified as argument")
		enableTLS     = flag.Bool("enable-tls", false, "enable TLS for the websocket connection")
		wsCert        = flag.String("ws-cert", "", "TLS cert for ws connection")
		wsKey         = flag.String("ws-key", "", "TLS key for ws connection")
		invoke        = flag.Bool("invoke", false, "invoke the container with NW support")
		wasiAddr      = flag.String("wasi-addr", "127.0.0.1:1234", "IP address used to communicate between wasi and network stack (valid only with invoke flag)")
		wasmtimeCli13 = flag.Bool("wasmtime-cli-13", false, "Use old wasmtime CLI (<= 13)")
	)
	mac := flag.String("mac", vmMAC, "mac address assigned to the container")
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		panic("specify args")
	}
	socketAddr := args[0]
	forwards := make(map[string]string)
	for _, p := range portFlags {
		parts := strings.Split(p, ":")
		switch len(parts) {
		case 3:
			forwards[strings.Join(parts[0:2], ":")] = strings.Join([]string{vmIP, parts[2]}, ":")
		case 2:
			forwards["0.0.0.0:"+parts[0]] = vmIP + ":" + parts[1]
		}
	}
	if *debug {
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.Discard)
	}
	log.Printf("port mapping: %+v\n", forwards)

	var gvisorForwards map[string]string
	if *listenWS {
		gvisorForwards = nil
	} else {
		gvisorForwards = forwards
	}

	config := &gvntypes.Configuration{
		Debug:             *debug,
		MTU:               1500,
		Subnet:            "192.168.127.0/24",
		GatewayIP:         gatewayIP,
		GatewayMacAddress: "5a:94:ef:e4:0c:dd",
		Forwards:          gvisorForwards,
		DHCPStaticLeases: map[string]string{
			*mac: vmIP,
		},
		NAT: map[string]string{
			"192.168.127.254": "127.0.0.1",
		},
		GatewayVirtualIPs: []string{"192.168.127.254"},
		Protocol:          gvntypes.QemuProtocol,
	}
	vn, err := gvnvirtualnetwork.New(config)
	if err != nil {
		panic(err)
	}

	portMappings := make(map[string]string)
	for localAddr, remoteAddr := range forwards {
		_, remotePort, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			log.Printf("invalid remote address %s: %v\n", remoteAddr, err)
			continue
		}
		portMappings[localAddr] = remotePort
	}

	if *invoke {
		go func() {
			fmt.Fprintf(os.Stderr, "waiting for NW initialization\n")
			var conn net.Conn
			for i := 0; i < 10; i++ {
				time.Sleep(1 * time.Second)
				log.Printf("connecting to NW...\n")
				conn, err = net.Dial("tcp", *wasiAddr)
				if err == nil {
					break
				}
				log.Printf("failed connecting to NW: %v\n", err)
			}
			if conn == nil {
				log.Fatalf("failed to connect to vm: lasterr=%d", err)
			}
			if err := vn.AcceptQemu(context.TODO(), conn); err != nil {
				log.Printf("failed AcceptQemu: %v\n", err)
			}
		}()
		var cmd *exec.Cmd
		if *wasmtimeCli13 {
			cmd = exec.Command("wasmtime", append([]string{"run", "--tcplisten=" + *wasiAddr, "--env='LISTEN_FDS=1'", "--"}, args...)...)
		} else {
			cmd = exec.Command("wasmtime", append([]string{"run", "-S", "preview2=n", "-S", "tcplisten=" + *wasiAddr, "--env='LISTEN_FDS=1'", "--"}, args...)...)
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			panic(err)
		}
		return
	}
	if *listenWS {
		var forwardersStarted sync.Once

		http.Handle("/", websocket.Handler(func(ws *websocket.Conn) {
			ws.PayloadType = websocket.BinaryFrame

			forwardersStarted.Do(func() {
				if len(portMappings) > 0 {
					go func() {
						containerIP := waitForContainerIP(vn)
						log.Printf("container IP detected: %s\n", containerIP)

						for localAddr, remotePort := range portMappings {
							remoteAddr := net.JoinHostPort(containerIP, remotePort)
							log.Printf("forwarding %s -> %s\n", localAddr, remoteAddr)
							go startPortForwarder(localAddr, remoteAddr, vn)
						}
					}()
				}
			})

			if err := vn.AcceptQemu(context.TODO(), ws); err != nil {
				log.Printf("forwarding finished: %v\n", err)
			}
		}))
		if *enableTLS {
			if err := http.ListenAndServeTLS(socketAddr, *wsCert, *wsKey, nil); err != nil {
				panic(err)
			}
		} else {
			if err := http.ListenAndServe(socketAddr, nil); err != nil {
				panic(err)
			}
		}
		return
	}
	conn, err := net.Dial("tcp", socketAddr)
	if err != nil {
		panic(err)
	}
	if err := vn.AcceptQemu(context.TODO(), conn); err != nil {
		panic(err)
	}
}

func waitForContainerIP(vn *gvnvirtualnetwork.VirtualNetwork) string {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-timeout:
			log.Printf("timeout waiting for DHCP lease, falling back to default IP %s\n", vmIP)
			return vmIP
		case <-ticker.C:
			if ip, err := getContainerIP(vn); err == nil {
				return ip
			}
		}
	}
}

func getContainerIP(vn *gvnvirtualnetwork.VirtualNetwork) (string, error) {
	req := httptest.NewRequest("GET", "/services/dhcp/leases", nil)
	w := httptest.NewRecorder()
	vn.ServicesMux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return "", fmt.Errorf("failed to query DHCP leases: status %d", w.Code)
	}

	var leases map[string]string
	if err := json.NewDecoder(w.Body).Decode(&leases); err != nil {
		return "", fmt.Errorf("failed to decode DHCP leases: %v", err)
	}

	for ip := range leases {
		if ip != gatewayIP {
			return ip, nil
		}
	}

	return "", fmt.Errorf("no container leases found")
}

func startPortForwarder(localAddr, remoteAddr string, vn *gvnvirtualnetwork.VirtualNetwork) {
	startPortForwarderWithContext(context.Background(), localAddr, remoteAddr, vn)
}

func startPortForwarderWithContext(ctx context.Context, localAddr, remoteAddr string, vn *gvnvirtualnetwork.VirtualNetwork) {
	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Printf("failed to listen on %s: %v\n", localAddr, err)
		return
	}
	defer listener.Close()
	log.Printf("forwarding %s -> %s\n", localAddr, remoteAddr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				log.Printf("port forwarder %s stopped\n", localAddr)
				return
			default:
				log.Printf("failed to accept connection on %s: %v\n", localAddr, err)
				return
			}
		}
		go handleConnection(conn, remoteAddr, vn)
	}
}

func handleConnection(clientConn net.Conn, remoteAddr string, vn *gvnvirtualnetwork.VirtualNetwork) {
	defer clientConn.Close()

	targetConn, err := vn.DialContextTCP(context.Background(), remoteAddr)
	if err != nil {
		log.Printf("failed to dial %s: %v\n", remoteAddr, err)
		return
	}
	defer targetConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(targetConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, targetConn)
		done <- struct{}{}
	}()
	<-done
}

type sliceFlags []string

func (f *sliceFlags) String() string {
	var s []string = *f
	return fmt.Sprintf("%v", s)
}

func (f *sliceFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}
