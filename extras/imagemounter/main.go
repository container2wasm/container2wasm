package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ctdcontainers "github.com/containerd/containerd/containers"
	ctdnamespaces "github.com/containerd/containerd/namespaces"
	ctdoci "github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/platforms"
	esgzcache "github.com/containerd/stargz-snapshotter/cache"
	"github.com/containerd/stargz-snapshotter/estargz"
	esgzconfig "github.com/containerd/stargz-snapshotter/fs/config"
	esgzreader "github.com/containerd/stargz-snapshotter/fs/reader"
	esgzremote "github.com/containerd/stargz-snapshotter/fs/remote"
	esgzsource "github.com/containerd/stargz-snapshotter/fs/source"
	esgzmetadata "github.com/containerd/stargz-snapshotter/metadata"
	esgzmetadatamemory "github.com/containerd/stargz-snapshotter/metadata/memory"
	esgztask "github.com/containerd/stargz-snapshotter/task"
	esgzcontainerdutil "github.com/containerd/stargz-snapshotter/util/containerdutil"
	gvntypes "github.com/containers/gvisor-tap-vsock/pkg/types"
	gvnvirtualnetwork "github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	p9staticfs "github.com/hugelgupf/p9/fsimpl/staticfs"
	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/p9"
	"github.com/moby/sys/user"
	digest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var proxyKey crypto.PrivateKey
var proxyCert *x509.Certificate

const proxyIP = "192.168.127.253"

const p9IP = "192.168.127.252"

func handleTunneling(w http.ResponseWriter, r *http.Request) {
	serverURL := r.URL
	cert, err := generateCert(serverURL.Hostname())
	if err != nil {
		log.Printf("failed to generate cert: %v\n", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	client_conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	go func() {
		defer client_conn.Close()
		l := newListener(&stringAddr{"tcp", proxyIP + ":80"}, client_conn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Scheme == "" {
					r.URL.Scheme = "https"
				}
				if r.URL.Host == "" {
					r.URL.Host = serverURL.Host
				}
				handleHTTP(w, r)
			}),
			// Disable HTTP/2 (FIXME)
			// https://pkg.go.dev/net/http#hdr-HTTP_2
			TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{*cert},
		}
		log.Printf("serving server for %s...\n", serverURL.Host)
		server.ServeTLS(l, "", "")
	}()
}

func generateCert(host string) (*tls.Certificate, error) {
	ser, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 60))
	if err != nil {
		return nil, err
	}
	cert := &x509.Certificate{
		SerialNumber: ser,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		cert.IPAddresses = append(cert.IPAddresses, ip)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	certD, err := x509.CreateCertificate(rand.Reader, cert, proxyCert, key.Public(), proxyKey)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{certD},
		PrivateKey:  key,
	}, nil
}

type listener struct {
	ch      net.Conn
	addr    net.Addr
	closeCh chan struct{}
}

func newListener(addr net.Addr, ch net.Conn) *listener {
	return &listener{
		ch:      ch,
		addr:    addr,
		closeCh: make(chan struct{}),
	}
}

func (l *listener) Accept() (net.Conn, error) {
	if l.ch == nil {
		<-l.closeCh
		return nil, fmt.Errorf("closed")
	}
	c := l.ch
	l.ch = nil
	return c, nil
}

func (l *listener) Close() error { close(l.closeCh); return nil }

func (l *listener) Addr() net.Addr { return l.addr }

type stringAddr struct {
	network string
	address string
}

func (a *stringAddr) Network() string { return a.network }
func (a *stringAddr) String() string  { return a.address }

type FetchParameters struct {
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type FetchResponse struct {
	Headers    map[string]string `json:"headers,omitempty"`
	Status     int               `json:"status,omitempty"`
	StatusText string            `json:"statusText,omitempty"`
}

func encodeHeader(h http.Header) map[string]string {
	res := make(map[string]string)
	for k, vs := range h {
		res[k] = strings.Join(vs, ", ")
	}
	return res
}

func decodeHeader(h map[string]string) http.Header {
	res := make(http.Header)
	for k, v := range h {
		res.Add(k, v)
	}
	return res
}

func httpRequestToFetchParameters(req *http.Request) *FetchParameters {
	return &FetchParameters{
		Method:  req.Method,
		Headers: encodeHeader(req.Header),
	}
}

func contentLengthFromHeaders(h http.Header) int64 {
	i, _ := strconv.Atoi(h.Get("Content-Length"))
	return int64(i)
}

func fetchResponseToHTTPResponse(req *http.Request, resp *FetchResponse) *http.Response {
	h := decodeHeader(resp.Headers)
	return &http.Response{
		Request:       req,
		Status:        resp.StatusText,
		StatusCode:    resp.Status,
		Header:        h,
		ContentLength: contentLengthFromHeaders(h), // required by containerd resolver
	}
}

//go:wasmimport env http_send
func http_send(addressP uint32, addresslen uint32, reqP, reqlen uint32, idP uint32) uint32

//go:wasmimport env http_writebody
func http_writebody(id uint32, chunkP, len uint32, nwrittenP uint32, isEOF uint32) uint32

//go:wasmimport env http_isreadable
func http_isreadable(id uint32, isOKP uint32) uint32

//go:wasmimport env http_recv
func http_recv(id uint32, respP uint32, bufsize uint32, respsizeP uint32, isEOFP uint32) uint32

//go:wasmimport env http_readbody
func http_readbody(id uint32, bodyP uint32, bufsize uint32, bodysizeP uint32, isEOFP uint32) uint32

func doHttpRoundTrip(req *http.Request) (*http.Response, error) {
	defer func() {
		if req.Body != nil {
			req.Body.Close()
		}
	}()

	address := req.URL.String()
	if address == "" {
		return nil, fmt.Errorf("specify destination address")
	}

	fetchReqD, err := json.Marshal(httpRequestToFetchParameters(req))
	if err != nil {
		return nil, err
	}
	if len(fetchReqD) == 0 {
		return nil, fmt.Errorf("empty request")
	}

	var id uint32
	res := http_send(
		uint32(uintptr(unsafe.Pointer(&[]byte(address)[0]))),
		uint32(len(address)),
		uint32(uintptr(unsafe.Pointer(&[]byte(fetchReqD)[0]))),
		uint32(len(fetchReqD)),
		uint32(uintptr(unsafe.Pointer(&id))),
	)
	if res != 0 {
		return nil, fmt.Errorf("failed to send request")
	}

	var buf []byte = make([]byte, 1048576)
	var isEOF uint32
	var nwritten uint32 = 0
	idx := 0
	chunksize := 0
	bodyR := io.Reader(req.Body)
	if bodyR == nil {
		bodyR = bytes.NewReader(make([]byte, 0))
	}
	for {
		if idx >= chunksize {
			// chunk is fully written. full another one.
			chunksize, err = bodyR.Read(buf)
			if err != nil && err != io.EOF {
				return nil, err
			}
			if err == io.EOF {
				isEOF = 1
			}
			idx = 0
		}
		res := http_writebody(
			id,
			uint32(uintptr(unsafe.Pointer(&[]byte(buf[idx:])[0]))),
			uint32(chunksize),
			uint32(uintptr(unsafe.Pointer(&nwritten))),
			isEOF,
		)
		if res != 0 {
			return nil, fmt.Errorf("failed to write request body")
		}
		idx += int(nwritten)
		if idx < chunksize {
			// not fully written. retry for the remaining.
			continue
		}
		if isEOF == 1 {
			break
		}
	}

	var isOK uint32 = 0
	for {
		res := http_isreadable(id, uint32(uintptr(unsafe.Pointer(&isOK))))
		if res != 0 {
			return nil, fmt.Errorf("response body is not readable")
		}
		if isOK == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	var respsize uint32
	var respFull []byte
	isEOF = 0
	for {
		res := http_recv(
			id,
			uint32(uintptr(unsafe.Pointer(&[]byte(buf)[0]))),
			uint32(len(buf)),
			uint32(uintptr(unsafe.Pointer(&respsize))),
			uint32(uintptr(unsafe.Pointer(&isEOF))),
		)
		if res != 0 {
			return nil, fmt.Errorf("failed to receive response")
		}
		respFull = append(respFull, buf[:int(respsize)]...)
		if isEOF == 1 {
			break
		}
	}
	var resp FetchResponse
	if err := json.Unmarshal(respFull, &resp); err != nil {
		return nil, err
	}
	respFull = nil

	isEOF = 0
	pr, pw := io.Pipe()
	go func() {
		var bodysize uint32
		for {
			res := http_readbody(
				id,
				uint32(uintptr(unsafe.Pointer(&[]byte(buf)[0]))),
				uint32(len(buf)),
				uint32(uintptr(unsafe.Pointer(&bodysize))),
				uint32(uintptr(unsafe.Pointer(&isEOF))),
			)
			if res != 0 {
				pw.CloseWithError(fmt.Errorf("failed to read response body"))
				return
			}
			if bodysize > 0 {
				if _, err := pw.Write(buf[:int(bodysize)]); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
			if isEOF == 1 {
				break
			}
		}
		buf = nil
		pw.Close()
	}()
	r := fetchResponseToHTTPResponse(req, &resp)
	r.Body = pr
	return r, nil
}

func handleHTTP(w http.ResponseWriter, req *http.Request) {
	resp, err := doHttpRoundTrip(req)
	if err != nil {
		log.Printf("failed to proxy request: %v\n", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

const (
	gatewayIP = "192.168.127.1"
	vmIP      = "192.168.127.3"
)

func main() {
	var listenFd int
	flag.IntVar(&listenFd, "net-listenfd", 0, "fd to listen for the connection")
	var certFd int
	flag.IntVar(&certFd, "certfd", 0, "fd to output cert")
	var certFile string
	flag.StringVar(&certFile, "certfile", "", "file to output cert")
	var debug bool
	flag.BoolVar(&debug, "debug", false, "debug log")
	var arch string
	flag.StringVar(&arch, "arch", "amd64", "target image architecture")
	var imageAddr string
	flag.StringVar(&imageAddr, "image-addr", "", "base address of image structured as OCI Image Layout")
	flag.Parse()

	if debug {
		log.SetOutput(os.Stdout)
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		log.SetOutput(io.Discard)
		logrus.SetLevel(logrus.FatalLevel)
	}

	var (
		imageServer *p9.Server
		waitImageServerInit func()
		err error
	)
	if imageAddr != "" {
		imageServer, waitImageServerInit, err = NewImageServer(context.TODO(), imageAddr, imagespec.Platform{
			Architecture: arch,
			OS:           "linux",
		})
		if err != nil {
			panic(err)
		}
	}

	ser, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 60))
	if err != nil {
		panic(err)
	}
	cert := &x509.Certificate{
		SerialNumber: ser,
		Subject: pkix.Name{
			CommonName:         proxyIP,
			OrganizationalUnit: []string{"proxy"},
			Organization:       []string{"proxy"},
			Country:            []string{"US"},
		},
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{proxyIP},
		IPAddresses:           []net.IP{net.ParseIP(proxyIP)},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	certD, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		panic(err)
	}
	var f io.WriteCloser
	if certFd != 0 {
		f = os.NewFile(uintptr(certFd), "")
	} else if certFile != "" {
		var err error
		f, err = os.OpenFile(certFile, os.O_RDWR|os.O_CREATE, 0755)
		if err != nil {
			panic(err)
		}
	} else {
		panic("specify cert destination")
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certD}); err != nil {
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certD},
		PrivateKey:  key,
	}
	proxyCert, err = x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		panic(err)
	}
	proxyKey = key

	config := &gvntypes.Configuration{
		Debug:             debug,
		MTU:               1500,
		Subnet:            "192.168.127.0/24",
		GatewayIP:         gatewayIP,
		GatewayMacAddress: "5a:94:ef:e4:0c:dd",
		GatewayVirtualIPs: []string{proxyIP, p9IP},
		Protocol:          gvntypes.QemuProtocol,
	}
	vn, err := gvnvirtualnetwork.New(config)
	if err != nil {
		panic(err)
	}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				handleTunneling(w, r)
			} else {
				handleHTTP(w, r)
			}
		}),
		// Disable HTTP/2 (FIXME)
		// https://pkg.go.dev/net/http#hdr-HTTP_2
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}

	go func() {
		s := server
		s.Addr = "0.0.0.0:80"
		l, err := vn.Listen("tcp", proxyIP+":80")
		if err != nil {
			panic(err)
		}
		log.Println("serving proxy with http")
		log.Fatal(server.Serve(l))
	}()
	go func() {
		s := server
		s.Addr = "0.0.0.0:443"
		l, err := vn.Listen("tcp", proxyIP+":443")
		if err != nil {
			panic(err)
		}
		s.TLSConfig = &tls.Config{
			Certificates: make([]tls.Certificate, 1),
		}
		s.TLSConfig.Certificates[0] = *tlsCert
		log.Println("serving proxy with https")
		log.Fatal(server.ServeTLS(l, "", ""))
	}()
	if imageAddr != "" {
		go func() {
			l, err := vn.Listen("tcp", p9IP+":80")
			if err != nil {
				panic(err)
			}
			if err != nil {
				panic(err)
			}
			if waitImageServerInit != nil {
				waitImageServerInit()
			}
			log.Fatal(imageServer.Serve(l))
		}()
	}
	ql, err := findListener(listenFd)
	if err != nil {
		panic(err)
	}
	if ql == nil {
		panic("socket fd not found")
	}
	qconn, err := ql.Accept()
	if err != nil {
		panic(err)
	}
	if err := vn.AcceptQemu(context.TODO(), qconn); err != nil {
		panic(err)
	}
}

func findListener(listenFd int) (net.Listener, error) {
	if listenFd == 0 {
		for preopenFd := 3; ; preopenFd++ {
			var stat syscall.Stat_t
			if err := syscall.Fstat(preopenFd, &stat); err != nil {
				var se syscall.Errno
				if errors.As(err, &se) && se == syscall.EBADF {
					err = nil
				}
				log.Printf("findListner failed (fd=%d): %v\n", preopenFd, err)
				return nil, err
			} else if stat.Filetype == syscall.FILETYPE_SOCKET_STREAM {
				listenFd = preopenFd
				break
			}
		}
	}
	syscall.SetNonblock(listenFd, true)
	f := os.NewFile(uintptr(listenFd), "")
	defer f.Close()
	log.Printf("using socket at fd=%d\n", listenFd)
	return net.FileListener(f)
}

func NewImageServer(ctx context.Context, imageAddr string, platform imagespec.Platform) (*p9.Server, func(), error) {
	config, rootNode, configD, waitInit, err := fsFromImage(ctx, imageAddr, platform, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch image %q: %w", imageAddr, err)
	}
	s, err := generateSpec(*config, rootNode)
	if err != nil {
		return nil, nil, err
	}
	specD, err := json.Marshal(s)
	if err != nil {
		return nil, nil, err
	}
	initNodes := make(map[string]p9.File)
	initNodes["rootfs"] = rootNode
	for name, o := range map[string][]p9staticfs.Option{
		"config": {
			p9staticfs.WithFile("config.json", string(specD)),
			p9staticfs.WithFile("imageconfig.json", string(configD)),
		},
	} {
		a, err := p9staticfs.New(o...)
		if err != nil {
			return nil, nil, err
		}
		n, err := a.Attach()
		if err != nil {
			return nil, nil, err
		}
		initNodes[name] = n
	}
	a, err := NewRouteNode(initNodes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create route node: %w", err)
	}
	return p9.NewServer(a), waitInit, nil
}

const (
	unixPasswdPath = "/etc/passwd"
	unixGroupPath  = "/etc/group"
)

func generateSpec(config imagespec.Image, rootNode p9.File) (_ *runtimespec.Spec, err error) {
	ic := config.Config
	ctdCtx := ctdnamespaces.WithNamespace(context.TODO(), "default")
	p := "linux/riscv64"
	if config.Architecture == "amd64" {
		p = "linux/amd64"
	}
	s, err := ctdoci.GenerateSpecWithPlatform(ctdCtx, nil, p, &ctdcontainers.Container{},
		ctdoci.WithHostNamespace(runtimespec.NetworkNamespace),
		ctdoci.WithoutRunMount,
		ctdoci.WithEnv(ic.Env),
		ctdoci.WithTTY, // TODO: make it configurable
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}
	if username := ic.User; username != "" {
		passwdR, passwdRClose, err := readFileFromNode(rootNode, unixPasswdPath)
		if err != nil {
			return nil, err
		}
		defer passwdRClose()
		groupR, groupRClose, err := readFileFromNode(rootNode, unixGroupPath)
		if err != nil {
			return nil, err
		}
		defer groupRClose()
		execUser, err := user.GetExecUser(username, nil, passwdR, groupR)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve username %q: %w", username, err)
		}
		s.Process.User.UID = uint32(execUser.Uid)
		s.Process.User.GID = uint32(execUser.Gid)
		for _, g := range execUser.Sgids {
			s.Process.User.AdditionalGids = append(s.Process.User.AdditionalGids, uint32(g))
		}
	}
	args := ic.Entrypoint
	if len(ic.Cmd) > 0 {
		args = append(args, ic.Cmd...)
	}
	if len(args) > 0 {
		s.Process.Args = args
	}
	if ic.WorkingDir != "" {
		s.Process.Cwd = ic.WorkingDir
	}

	// TODO: support seccomp and apparmor
	s.Linux.Seccomp = nil
	s.Root = &runtimespec.Root{
		Path: "/run/rootfs",
	}
	// TODO: ports
	return s, nil
}

func fsFromImage(ctx context.Context, addr string, platform imagespec.Platform, insecure bool) (*imagespec.Image, *Node, []byte, func(), error) {
	var layers []NodeLayer
	var config imagespec.Image
	var configData []byte
	var waitInit func()
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		log.Printf("Pulling from HTTP server %q\n", addr)
		manifest, image, configD, err := fetchManifestAndConfigOCILayout(ctx, addr, platform)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		config = image
		configData = configD
		layers, waitInit, err = fetchLayers(ctx, manifest, EStargzLayerConfig{
			NoBackgroundFetch: true,
			PrefetchTimeout:   5 * time.Second,
			// NoPrefetch:        true,
		}, nil, map[string]esgzremote.Handler{
			"url-reader": &layerOCILayoutURLHandler{addr},
		}, reference.Spec{}, newLayerOCILayoutExternalReaderAt(addr))
		if err != nil {
			return nil, nil, nil, nil, err
		}
	} else {
		log.Printf("Pulling from registry %q\n", addr)
		refspec, err := reference.Parse(addr)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		manifest, image, configD, fetcher, err := fetchManifestAndConfigRegistry(ctx, refspec, platform)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		config = image
		configData = configD
		layers, waitInit, err = fetchLayers(ctx, manifest, EStargzLayerConfig{
			NoBackgroundFetch: true,
			PrefetchTimeout:   5 * time.Second,
			// NoPrefetch:        true,
		}, wasmRegistryHosts, nil, refspec, func(desc imagespec.Descriptor, withDecompression bool) (io.ReaderAt, error) {
			r, err := fetcher.Fetch(ctx, desc)
			if err != nil {
				return nil, err
			}
			defer r.Close()
			if withDecompression {
				raw := r
				zr, err := newWasmDecompressor(raw)
				if err != nil {
					return nil, err
				}
				r = &readerWithCloser{zr, func() error {
					return raw.Close()
				}}
			}

			var rs [][]byte
			var curoff int
			var chunkSize = 10 * 1024 * 1024
			b := make([]byte, chunkSize)
			for {
				n, err := r.Read(b[curoff:])
				curoff += n
				if err != nil {
					if err == io.EOF {
						rs = append(rs, b[:curoff])
						break
					}
					return nil, err
				}
				if curoff == len(b) {
					rs = append(rs, b)
					curoff = 0
					b = make([]byte, chunkSize)
				}
			}
			r.Close()
			var totalLen int
			for _, s := range rs {
				totalLen += len(s)
			}
			data := make([]byte, totalLen)
			var i int
			for _, s := range rs {
				i += copy(data[i:], s)
			}
			return io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))), nil
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	imgFS := &applier{}

	for _, l := range layers {
		if err := imgFS.ApplyNodes(l); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	imgFS.addDots()
	return &config, imgFS.n, configData, waitInit, nil
}

var defaultClient = &http.Client{Transport: wasmRoundTripper{}}

func fetchManifestAndConfigRegistry(ctx context.Context, refspec reference.Spec, platform platforms.Platform) (imagespec.Manifest, imagespec.Image, []byte, remotes.Fetcher, error) {
	resolver := docker.NewResolver(docker.ResolverOptions{
		Hosts: func(host string) ([]docker.RegistryHost, error) {
			if host != refspec.Hostname() {
				return nil, fmt.Errorf("unexpected host %q for image ref %q", host, refspec.String())
			}
			return wasmRegistryHosts(refspec)
		},
	})
	_, img, err := resolver.Resolve(ctx, refspec.String())
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	fetcher, err := resolver.Fetcher(ctx, refspec.String())
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	manifest, err := esgzcontainerdutil.FetchManifestPlatform(ctx, fetcher, img, platform)
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	r, err := fetcher.Fetch(ctx, manifest.Config)
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	defer r.Close()
	configD, err := io.ReadAll(r)
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	var config imagespec.Image
	if err := json.NewDecoder(bytes.NewReader(configD)).Decode(&config); err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, nil, err
	}
	return manifest, config, configD, fetcher, err
}

func fetchManifestAndConfigOCILayout(ctx context.Context, addr string, platform platforms.Platform) (imagespec.Manifest, imagespec.Image, []byte, error) {
	c := defaultClient

	// Fetch index
	resp, err := c.Get(addr + "/index.json")
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	var index imagespec.Index
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	var target digest.Digest
	found := false
	for _, m := range index.Manifests {
		p := platform
		if m.Platform != nil {
			p = *m.Platform
		}
		if platforms.NewMatcher(platform).Match(p) {
			target, found = m.Digest, true
			break
		}
	}
	if !found {
		return imagespec.Manifest{}, imagespec.Image{}, nil, fmt.Errorf("manifest not found for platform %v", platform)
	}

	// Fetch manifest
	resp, err = c.Get(addr + "/blobs/sha256/" + target.Encoded())
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	var manifest imagespec.Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Fetch config
	resp, err = c.Get(addr + "/blobs/sha256/" + manifest.Config.Digest.Encoded())
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	var config imagespec.Image
	configD, err := io.ReadAll(resp.Body)
	if err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	if err := json.NewDecoder(bytes.NewReader(configD)).Decode(&config); err != nil {
		return imagespec.Manifest{}, imagespec.Image{}, nil, err
	}
	resp.Body.Close()

	return manifest, config, configD, nil
}

func fetchLayers(ctx context.Context, manifest imagespec.Manifest, config EStargzLayerConfig, hosts esgzsource.RegistryHosts, handlers map[string]esgzremote.Handler, refspec reference.Spec, unlazyReader func(imagespec.Descriptor, bool) (io.ReaderAt, error)) (layers []NodeLayer, waitInit func(), err error) {
	layers, waitInit, err = getEStargzLayers(ctx, manifest, config, hosts, handlers, refspec)
	if err == nil {
		return layers, waitInit, nil
	}

	log.Printf("falling back to gzip mode (previous error: %v)\n", err)
	layers, err = getTarLayers(ctx, manifest, true, unlazyReader)
	if err == nil {
		return layers, nil, nil
	}

	log.Printf("falling back to tar mode (previous error: %v)\n", err)
	layers, err = getTarLayers(ctx, manifest, false, unlazyReader)
	if err == nil {
		return layers, nil, nil
	}

	return nil, nil, fmt.Errorf("failed to fetch layers (last error: %v)", err)
}

func getTarLayers(ctx context.Context, manifest imagespec.Manifest, withDecompression bool, getReader func(imagespec.Descriptor, bool) (io.ReaderAt, error)) ([]NodeLayer, error) {
	layers := make([]NodeLayer, len(manifest.Layers))
	q := new(qidSet)
	eg, _ := errgroup.WithContext(ctx)
	for i, l := range manifest.Layers {
		i, l := i, l
		eg.Go(func() error {
			r, err := getReader(l, withDecompression)
			if err != nil {
				return err
			}
			n, err := newTarNode(q, r)
			if err != nil {
				return err
			}
			layers[i] = *n
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return layers, nil
}

func newTarNode(q *qidSet, trRaw io.ReaderAt) (*NodeLayer, error) {
	pw, err := NewPositionWatcher(trRaw)
	if err != nil {
		return nil, fmt.Errorf("Failed to make position watcher: %w", err)
	}
	tr := tar.NewReader(pw)

	// Walk functions for nodes
	getormake := func(n *Node, base string) (c *Node, err error) {
		var ok bool
		if c, ok = n.children[base]; !ok {
			// Make temporary dummy node (directory).
			c = &Node{
				qid: p9.QID{
					Type: p9.TypeDir,
					Path: q.newQID(),
				},
				attr: p9.Attr{
					Mode: p9.ModeDirectory,
				},
			}
			if n.children == nil {
				n.children = make(map[string]*Node)
			}
			n.children[base] = c
		}
		return c, nil
	}

	getnode := func(n *Node, base string) (c *Node, err error) {
		var ok bool
		if c, ok = n.children[base]; !ok {
			return nil, fmt.Errorf("Node %q not found", base)
		}
		return c, nil
	}

	var whiteouts []string
	var opaqueWhiteouts []string

	n := &Node{
		qid: p9.QID{
			Type: p9.TypeDir,
			Path: q.newQID(),
		},
		attr: p9.Attr{
			Mode: p9.ModeDirectory,
		},
	} // This layer's files. Will be merged with lower

	// Walk through all nodes.
	for {
		// Fetch and parse next header.
		h, err := tr.Next()
		if err != nil {
			if err != io.EOF {
				return nil, fmt.Errorf("failed to parse tar file: %w", err)
			}
			break
		}
		var (
			fullname     = filepath.Clean(h.Name)
			dir, base    = filepath.Split(fullname)
			parentDir    *Node
			existingNode *Node
			// TODO :support xattr
		)
		if parentDir, err = walkDown(n, dir, getormake); err != nil {
			return nil, fmt.Errorf("failed to make dummy nodes: %w", err)
		}
		existingNode, _ = parentDir.children[base]
		switch {
		case existingNode != nil:
			if !existingNode.attr.Mode.IsDir() {
				return nil, fmt.Errorf("node %q is placeholder but not a directory", fullname)
			}
			// This is "placeholder node" which has been registered in previous loop as
			// an intermediate directory. Now we update it with real one.
			attr, qid := headerToAttr(h)
			qid.Path = q.newQID()
			existingNode.attr = attr
			existingNode.qid = qid
		case strings.HasPrefix(base, whiteoutPrefix):
			if base == whiteoutOpaqueDir {
				opaqueWhiteouts = append(opaqueWhiteouts, fullname)
			} else {
				whiteouts = append(whiteouts, fullname)
			}
		case h.Typeflag == tar.TypeLink:
			if h.Linkname == "" {
				return nil, fmt.Errorf("Linkname of hardlink %q is not found", fullname)
			}
			// This node is a hardlink. Same as major tar tools(GNU tar etc.),
			// we pretend that the target node of this hard link has already been appeared.
			target, err := walkDown(n, filepath.Clean(h.Linkname), getnode)
			if err != nil {
				return nil, fmt.Errorf("hardlink(%q ==> %q) is not found: %w",
					fullname, h.Linkname, err)
			}
			target.attr.NLink++
			if parentDir.children == nil {
				parentDir.children = make(map[string]*Node)
			}
			parentDir.children[base] = target
		default:
			// Normal node so simply create it.
			if parentDir.children == nil {
				parentDir.children = make(map[string]*Node)
			}
			attr, qid := headerToAttr(h)
			qid.Path = q.newQID()
			parentDir.children[base] = &Node{
				qid:  qid,
				attr: attr,
				link: h.Linkname,
				r:    io.NewSectionReader(trRaw, pw.CurrentPos(), h.Size),
			}
		}

		continue
	}

	return &NodeLayer{node: n, whiteouts: whiteouts, opaqueWhiteouts: opaqueWhiteouts}, nil
}

type EStargzLayerConfig struct {
	DisableChunkVerify bool
	MaxConcurrency     int64
	PrefetchTimeout    time.Duration
	NoPrefetch         bool
	NoBackgroundFetch  bool
}

func wasmRegistryHosts(ref reference.Spec) (hosts []docker.RegistryHost, _ error) {
	host := ref.Hostname()
	client := defaultClient
	config := docker.RegistryHost{
		Client:       client,
		Host:         host,
		Scheme:       "https",
		Path:         "/v2",
		Capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve,
		Authorizer:   docker.NewDockerAuthorizer(docker.WithAuthClient(client)),
	}
	if localhost, _ := docker.MatchLocalhost(config.Host); localhost {
		config.Scheme = "http"
	}
	if config.Host == "docker.io" {
		config.Host = "registry-1.docker.io"
	}
	hosts = append(hosts, config)
	return
}

func getEStargzLayers(ctx context.Context, manifest imagespec.Manifest, config EStargzLayerConfig, hosts esgzsource.RegistryHosts, handlers map[string]esgzremote.Handler, refspec reference.Spec) ([]NodeLayer, func(), error) {
	layers := make([]NodeLayer, len(manifest.Layers))
	checkChunkDigests := config.DisableChunkVerify
	maxConcurrency := config.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = int64(2)
	}
	prefetchTimeout := config.PrefetchTimeout
	if prefetchTimeout == 0 {
		prefetchTimeout = time.Second * 10
	}
	noPrefetch := config.NoPrefetch
	noBackgroundFetch := config.NoBackgroundFetch
	blobCache := esgzcache.NewMemoryCache()
	readerCache := esgzcache.NewMemoryCache()
	esgzresolver := esgzremote.NewResolver(esgzconfig.BlobConfig{}, handlers)
	tm := esgztask.NewBackgroundTaskManager(maxConcurrency, 5*time.Second)
	prefetcheg, _ := errgroup.WithContext(context.TODO())
	eg, _ := errgroup.WithContext(ctx)
	for i, l := range manifest.Layers {
		i, l := i, l
		eg.Go(func() error {
			b, err := esgzresolver.Resolve(ctx, hosts, refspec, l, blobCache) // TODO: use directory cache
			if err != nil {
				return err
			}
			mr, err := esgzmetadatamemory.NewReader(
				io.NewSectionReader(readerAtFunc(func(p []byte, offset int64) (int, error) {
					tm.DoPrioritizedTask()
					defer tm.DonePrioritizedTask()
					return b.ReadAt(p, offset)
				}), 0, b.Size()),
				esgzmetadata.WithDecompressors(newGzipDecompressor()),
			)
			if err != nil {
				return err
			}
			vr, err := esgzreader.NewReader(mr, readerCache, l.Digest) // TODO: use directory cache
			if err != nil {
				return err
			}
			if !noPrefetch {
				prefetchWaiter := newWaiter()
				prefetcheg.Go(func() error {
					if err := prefetchWaiter.wait(prefetchTimeout); err != nil {
						log.Printf("failed to wait for prefetch: %v\n", err)
					}
					return nil
				})
				go func() {
					if err := esgzPrefetch(ctx, tm, prefetchWaiter, b, vr); err != nil {
						log.Printf("failed to prefetch layer %v\n", l.Digest)
						return
					}
					log.Printf("completed prefetch of layer %v\n", l.Digest)
				}()
			}
			if !noBackgroundFetch {
				go func() {
					if err := esgzBackgroundFetch(ctx, tm, b, vr); err != nil {
						log.Printf("failed background fetching of layer %v\n", l.Digest)
						return
					}
					log.Printf("completed background fetch of layer %v\n", l.Digest)
				}()
			}
			var rr esgzreader.Reader
			if checkChunkDigests {
				tocDgstStr, ok := l.Annotations[estargz.TOCJSONDigestAnnotation]
				if !ok {
					return fmt.Errorf("digest of TOC JSON must be passed")
				}
				tocDgst, err := digest.Parse(tocDgstStr)
				if err != nil {
					return fmt.Errorf("invalid TOC digest: %v: %w", tocDgst, err)
				}
				rr, err = vr.VerifyTOC(tocDgst)
				if err != nil {
					return fmt.Errorf("invalid estargz layer: %w", err)
				}
			} else {
				rr = vr.SkipVerify() // TODO: verify TOC digest using layer annotation
			}
			n, err := newESGZNode(rr, rr.Metadata().RootID(), uint32(i), "")
			if err != nil {
				return err
			}
			layers[i] = *n
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}
	return layers, func() {
		prefetcheg.Wait()
	}, nil
}

func esgzPrefetch(ctx context.Context, tm *esgztask.BackgroundTaskManager, prefetchWaiter *waiter, blob esgzremote.Blob, r *esgzreader.VerifiableReader) error {
	tm.DoPrioritizedTask()
	defer tm.DonePrioritizedTask()
	defer prefetchWaiter.done() // Notify the completion
	// Measuring the total time to complete prefetch (use defer func() because l.Info().PrefetchSize is set later)
	rootID := r.Metadata().RootID()
	var prefetchSize int64
	if _, _, err := r.Metadata().GetChild(rootID, estargz.NoPrefetchLandmark); err == nil {
		// do not prefetch this layer
		return nil
	} else if id, _, err := r.Metadata().GetChild(rootID, estargz.PrefetchLandmark); err == nil {
		offset, err := r.Metadata().GetOffset(id)
		if err != nil {
			return fmt.Errorf("failed to get offset of prefetch landmark: %w", err)
		}
		// override the prefetch size with optimized value
		prefetchSize = offset
	}

	// Fetch the target range
	if err := blob.Cache(0, prefetchSize); err != nil {
		return fmt.Errorf("failed to prefetch layer: %w", err)
	}

	// Cache uncompressed contents of the prefetched range
	if err := r.Cache(esgzreader.WithFilter(func(offset int64) bool {
		return offset < prefetchSize // Cache only prefetch target
	})); err != nil {
		return fmt.Errorf("failed to cache prefetched layer: %w", err)
	}

	return nil
}

func esgzBackgroundFetch(ctx context.Context, tm *esgztask.BackgroundTaskManager, blob esgzremote.Blob, r *esgzreader.VerifiableReader) error {
	br := io.NewSectionReader(readerAtFunc(func(p []byte, offset int64) (retN int, retErr error) {
		tm.InvokeBackgroundTask(func(ctx context.Context) {
			retN, retErr = blob.ReadAt(
				p,
				offset,
				esgzremote.WithContext(ctx), // Make cancellable
				esgzremote.WithCacheOpts(esgzcache.Direct()), // Do not pollute mem cache
			)
		}, 120*time.Second)
		return
	}), 0, blob.Size())
	return r.Cache(
		esgzreader.WithReader(br),                    // Read contents in background
		esgzreader.WithCacheOpts(esgzcache.Direct()), // Do not pollute mem cache
	)
}

type routeAttacher struct {
	node *bindNode
}

var _ p9.Attacher = &routeAttacher{}

func NewRouteNode(children map[string]p9.File) (p9.Attacher, error) {
	r := &RouteNode{
		qid: p9.QID{
			Type: p9.TypeDir,
			Path: 0,
		},
		attr: p9.Attr{
			Mode: p9.ModeDirectory,
		},
		children: make(map[string]*bindNode),
	}
	fs := &bindFS{}
	i := 0
	for name, f := range children {
		r.children[name] = &bindNode{
			children: make(map[p9.QID]p9.File), // TODO: do it lazy
			bind:     make(map[p9.QID]p9.QID),  // TODO: do it lazy
			f:        f,
			fs:       fs,
		}
		i++
	}
	bindRoot := &bindNode{
		children: make(map[p9.QID]p9.File), // TODO: do it lazy
		bind:     make(map[p9.QID]p9.QID),  // TODO: do it lazy
		f:        r,
		fs:       fs,
	}
	return &routeAttacher{node: bindRoot}, nil
}

func (a *routeAttacher) Attach() (p9.File, error) {
	return a.node, nil
}

type bindFS struct {
	curQID uint64
	mu     sync.Mutex
}

func (fs *bindFS) newQID() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.curQID++
	return fs.curQID
}

type bindNode struct {
	p9.DefaultWalkGetAttr
	templatefs.NoopFile
	templatefs.ReadOnlyFile
	templatefs.ReadOnlyDir

	children map[p9.QID]p9.File // keyed by global qid
	bind     map[p9.QID]p9.QID  // local to global (itself or chlidren)
	// qid  p9.QID
	// attr p9.Attr

	f p9.File

	fs *bindFS
}

// TODO: reuse Node.Walk
func (n *bindNode) Walk(names []string) ([]p9.QID, p9.File, error) {
	log.Println("bindNode.Walk", names)
	qids, f, err := n.f.Walk(names)
	if err != nil {
		return nil, nil, err
	}

	// Translate to or register the global qid
	var gqids []p9.QID
	for _, q := range qids {
		gq, ok := n.bind[q]
		if !ok {
			gq = p9.QID{
				Type:    q.Type,
				Version: q.Version,
				Path:    n.fs.newQID(),
			}
			n.bind[q] = gq
		}
		gqids = append(gqids, gq)
	}

	lqid, _, _, err := f.GetAttr(p9.AttrMask{})
	if err != nil {
		return nil, nil, err
	}
	gq, ok := n.bind[lqid]
	if !ok {
		gq = p9.QID{
			Type:    lqid.Type,
			Version: lqid.Version,
			Path:    n.fs.newQID(),
		}
		n.bind[lqid] = gq
	}
	gf, ok := n.children[gq]
	if !ok {
		gf = &bindNode{
			children: make(map[p9.QID]p9.File), // TODO: do it lazy
			bind:     make(map[p9.QID]p9.QID),  // TODO: do it lazy
			f:        f,
			fs:       n.fs,
		}
	}

	return gqids, gf, nil
}

func (n *bindNode) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	log.Println("bindNode.GetAttr", req)
	qid, mask, attr, err := n.f.GetAttr(req)
	if err != nil {
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
	}
	gq, ok := n.bind[qid]
	if !ok {
		gq = p9.QID{
			Type:    qid.Type,
			Version: qid.Version,
			Path:    n.fs.newQID(),
		}
		n.bind[qid] = gq
	}
	return gq, mask, attr, nil
}

// TODO: reuse Node.Readdir
func (n *bindNode) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	log.Println("bindNode.Readdir", offset, count)
	ents, err := n.f.Readdir(offset, count)
	if err != nil {
		return nil, err
	}
	log.Println("bindNode.Readdir => len(ents) =", len(ents))

	var gents p9.Dirents
	for _, e := range ents {
		log.Println("bindNode.Readdir => e =", e)
		gq, ok := n.bind[e.QID]
		if !ok {
			gq = p9.QID{
				Type:    e.QID.Type,
				Version: e.QID.Version,
				Path:    n.fs.newQID(),
			}
			n.bind[e.QID] = gq
		}
		e2 := e // copy
		e2.QID = gq
		gents = append(gents, e2)
	}
	log.Println("bindNode.Readdir => len(gents) =", len(gents))
	return gents, nil
}

func (n *bindNode) Close() error {
	return nil
}

func (n *bindNode) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	log.Println("bindNode.Open", mode)
	// qid, s, err := n.f.Open(mode)
	qid, s, err := n.f.Open(p9.ReadOnly) // TODO: pass the original mode; but it causes EROFS error when reading files
	if err != nil {
		return p9.QID{}, 0, err
	}
	gq, ok := n.bind[qid]
	if !ok {
		gq = p9.QID{
			Type:    qid.Type,
			Version: qid.Version,
			Path:    n.fs.newQID(),
		}
		n.bind[qid] = gq
	}
	return gq, s, nil
}

func (n *bindNode) ReadAt(p []byte, offset int64) (int, error) {
	return n.f.ReadAt(p, offset)
}

func (n *bindNode) Readlink() (string, error) {
	return n.f.Readlink()
}

func (n *bindNode) FSync() error {
	return syscall.EROFS
}

func (n *bindNode) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	return syscall.EROFS
}

func (n *bindNode) Remove() error {
	return syscall.EROFS
}

func (n *bindNode) Rename(directory p9.File, name string) error {
	return syscall.EROFS
}

func (n *bindNode) WriteAt(p []byte, offset int64) (int, error) {
	return 0, syscall.EROFS
}

func (n *bindNode) Flush() error {
	return nil
}

func (n *bindNode) Create(name string, mode p9.OpenFlags, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.File, p9.QID, uint32, error) {
	return nil, p9.QID{}, 0, syscall.EROFS
}

func (n *bindNode) Mkdir(name string, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *bindNode) Symlink(oldname string, newname string, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *bindNode) Link(target p9.File, newname string) error {
	return syscall.EROFS
}

func (n *bindNode) Mknod(name string, mode p9.FileMode, major uint32, minor uint32, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *bindNode) RenameAt(oldname string, newdir p9.File, newname string) error {
	return syscall.EROFS
}

func (n *bindNode) UnlinkAt(name string, flags uint32) error {
	return syscall.EROFS
}

type RouteNode struct {
	p9.DefaultWalkGetAttr
	templatefs.NoopFile
	templatefs.ReadOnlyFile
	templatefs.ReadOnlyDir

	children map[string]*bindNode
	qid      p9.QID
	attr     p9.Attr
}

func (n *RouteNode) Walk(names []string) ([]p9.QID, p9.File, error) {
	log.Println("RouteNode.Walk", names)
	if len(names) == 0 {
		return []p9.QID{n.qid}, n, nil
	}
	var qids []p9.QID
	last := p9.File(n)
	for _, name := range names {
		c, ok := n.children[name]
		if !ok {
			return nil, nil, os.ErrNotExist
		}
		cqid, _, _, err := c.GetAttr(p9.AttrMask{})
		if err != nil {
			return nil, nil, err
		}
		qids = append(qids, cqid)
		last = c
	}
	return qids, last, nil
}

func (n *RouteNode) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	log.Println("RouteNode.GetAttr", req)
	return n.qid, req, n.attr, nil
}

// TODO: reuse Node.Readdir
func (n *RouteNode) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	log.Println("RouteNode.Readdir", offset, count)
	if offset > uint64(len(n.children)) {
		return nil, nil
	}
	end := int(offset) + int(count)
	if end > int(len(n.children)) {
		end = int(len(n.children))
	}

	var names []string
	for k := range n.children {
		names = append(names, k)
	}
	sort.Strings(names)

	var ents []p9.Dirent
	for i, name := range names[offset:end] {
		c, ok := n.children[name]
		if !ok {
			return nil, fmt.Errorf("child %q not found", name)
		}
		cqid, _, _, err := c.GetAttr(p9.AttrMask{})
		if err != nil {
			return nil, err
		}
		ents = append(ents, p9.Dirent{
			QID: cqid,
			// returned offset needs to start from 1 according to p9.localfs
			Offset: offset + uint64(i) + 1,
			Type:   cqid.Type,
			Name:   name,
		})
	}

	return ents, nil
}

func (n *RouteNode) Close() error {
	return nil
}

const (
	// p9DefaultBlockSize = 4096
	p9DefaultBlockSize = 53248
)

func (n *RouteNode) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	return n.qid, p9DefaultBlockSize, nil
}

func (n *RouteNode) ReadAt(p []byte, offset int64) (int, error) {
	return 0, os.ErrInvalid
}

func (n *RouteNode) Readlink() (string, error) {
	return "", nil
}

func (n *RouteNode) FSync() error {
	return syscall.EROFS
}

func (n *RouteNode) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	return syscall.EROFS
}

func (n *RouteNode) Remove() error {
	return syscall.EROFS
}

func (n *RouteNode) Rename(directory p9.File, name string) error {
	return syscall.EROFS
}

func (n *RouteNode) WriteAt(p []byte, offset int64) (int, error) {
	return 0, syscall.EROFS
}

func (n *RouteNode) Flush() error {
	return nil
}

func (n *RouteNode) Create(name string, mode p9.OpenFlags, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.File, p9.QID, uint32, error) {
	return nil, p9.QID{}, 0, syscall.EROFS
}

func (n *RouteNode) Mkdir(name string, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *RouteNode) Symlink(oldname string, newname string, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *RouteNode) Link(target p9.File, newname string) error {
	return syscall.EROFS
}

func (n *RouteNode) Mknod(name string, mode p9.FileMode, major uint32, minor uint32, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *RouteNode) RenameAt(oldname string, newdir p9.File, newname string) error {
	return syscall.EROFS
}

func (n *RouteNode) UnlinkAt(name string, flags uint32) error {
	return syscall.EROFS
}

type attacher struct {
	node p9.File
}

var _ p9.Attacher = &attacher{}

func NodeAttacher(n p9.File) p9.Attacher {
	return &attacher{node: n}
}

func (a *attacher) Attach() (p9.File, error) {
	return a.node, nil
}

type Node struct {
	p9.DefaultWalkGetAttr
	templatefs.NoopFile
	templatefs.ReadOnlyFile
	templatefs.ReadOnlyDir

	qid  p9.QID
	attr p9.Attr
	link string

	r io.ReaderAt

	children map[string]*Node
}

func (n *Node) Walk(names []string) ([]p9.QID, p9.File, error) {
	if len(names) == 0 {
		return []p9.QID{n.qid}, n, nil
	}
	var qids []p9.QID
	last := n
	for _, name := range names {
		c, ok := n.children[name]
		if !ok {
			return nil, nil, os.ErrNotExist
		}
		qids = append(qids, c.qid)
		last = c
	}
	return qids, last, nil
}

func (n *Node) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	return n.qid, req, n.attr, nil
}

func (n *Node) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	if offset > uint64(len(n.children)) {
		return nil, nil
	}
	end := int(offset) + int(count)
	if end > int(len(n.children)) {
		end = int(len(n.children))
	}

	var names []string
	for k := range n.children {
		names = append(names, k)
	}
	sort.Strings(names)

	var ents []p9.Dirent
	for i, name := range names[offset:end] {
		c, ok := n.children[name]
		if !ok {
			return nil, fmt.Errorf("child %q not found", name)
		}
		ents = append(ents, p9.Dirent{
			QID: c.qid,
			// returned offset needs to start from 1 according to p9.localfs
			Offset: offset + uint64(i) + 1,
			Type:   c.qid.Type,
			Name:   name,
		})
	}

	return ents, nil
}

func (n *Node) Close() error {
	return nil
}

func (n *Node) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	return n.qid, p9DefaultBlockSize, nil
}

func (n *Node) ReadAt(p []byte, offset int64) (int, error) {
	return n.r.ReadAt(p, offset)
}

func (n *Node) Readlink() (string, error) {
	return n.link, nil
}

func (n *Node) FSync() error {
	return syscall.EROFS
}

func (n *Node) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	return syscall.EROFS
}

func (n *Node) Remove() error {
	return syscall.EROFS
}

func (n *Node) Rename(directory p9.File, name string) error {
	return syscall.EROFS
}

func (n *Node) WriteAt(p []byte, offset int64) (int, error) {
	return 0, syscall.EROFS
}

func (n *Node) Flush() error {
	return nil
}

func (n *Node) Create(name string, mode p9.OpenFlags, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.File, p9.QID, uint32, error) {
	return nil, p9.QID{}, 0, syscall.EROFS
}

func (n *Node) Mkdir(name string, permissions p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *Node) Symlink(oldname string, newname string, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *Node) Link(target p9.File, newname string) error {
	return syscall.EROFS
}

func (n *Node) Mknod(name string, mode p9.FileMode, major uint32, minor uint32, _ p9.UID, _ p9.GID) (p9.QID, error) {
	return p9.QID{}, syscall.EROFS
}

func (n *Node) RenameAt(oldname string, newdir p9.File, newname string) error {
	return syscall.EROFS
}

func (n *Node) UnlinkAt(name string, flags uint32) error {
	return syscall.EROFS
}

type applier struct {
	n *Node //root
}

func (a *applier) ApplyNodes(nodes NodeLayer) error {
	getnode := func(n *Node, base string) (c *Node, err error) {
		var ok bool
		if c, ok = n.children[base]; !ok {
			return nil, fmt.Errorf("Node %q not found", base)
		}
		return c, nil
	}
	for _, w := range nodes.whiteouts {
		dir, base := filepath.Split(w)
		if _, err := walkDown(a.n, filepath.Join(dir, base[len(whiteoutPrefix):]), getnode); err == nil {
			p, err := walkDown(a.n, dir, getnode)
			if err != nil {
				log.Printf("parent node of whiteout %q is not found: %v\n", w, err)
			} else {
				delete(p.children, base)
			}
		}
	}
	for _, w := range nodes.opaqueWhiteouts {
		dir, _ := filepath.Split(w)
		p, err := walkDown(a.n, dir, getnode)
		if err != nil {
			log.Printf("parent node of opaque whiteout %q is not found: %v\n", w, err)
		} else {
			p.children = nil
		}
	}
	var err error
	a.n, err = mergeNode(a.n, nodes.node)
	if err != nil {
		return err
	}
	return nil
}

func (a *applier) addDots() {
	addDots(a.n, nil)
}

type qidSet struct {
	curQID uint64
	mu     sync.Mutex
}

func (q *qidSet) newQID() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.curQID++
	return q.curQID
}

const (
	whiteoutPrefix    = ".wh."
	whiteoutOpaqueDir = whiteoutPrefix + whiteoutPrefix + ".opq"
)

type NodeLayer struct {
	node            *Node
	whiteouts       []string
	opaqueWhiteouts []string
}

func mergeNode(a, b *Node) (*Node, error) {
	if a == nil {
		return b, nil
	}
	if b == nil {
		return a, nil
	}
	ret := b // b is prioritized
	for base, ac := range a.children {
		bc, _ := b.children[base]
		c2, err := mergeNode(ac, bc) // TODO: parallelize
		if err != nil {
			return nil, err
		}
		if c2 != nil {
			if b.children == nil {
				b.children = make(map[string]*Node)
			}
			b.children[base] = c2
		}
	}
	return ret, nil
}

func addDots(a *Node, parent *Node) *Node {
	if a == nil {
		return nil
	}
	if a.qid.Type == p9.TypeDir {
		if a.children == nil {
			a.children = make(map[string]*Node)
		}
		if _, ok := a.children["."]; !ok {
			a.attr.NLink++
			a.children["."] = a
		}
		if _, ok := a.children[".."]; !ok {
			if parent != nil {
				parent.attr.NLink++
				a.children[".."] = parent
			}
		}
	}
	for name, c := range a.children {
		if name == "." || name == ".." {
			continue
		}
		addDots(c, a)
	}
	return a
}

type walkFunc func(n *Node, base string) (*Node, error)

func walkDown(n *Node, path string, walkFn walkFunc) (ino *Node, err error) {
	ino = n
	for _, comp := range strings.Split(path, "/") {
		if len(comp) == 0 {
			continue
		}
		if ino == nil {
			return nil, fmt.Errorf("corresponding node of %q is not found", comp)
		}
		if ino, err = walkFn(ino, comp); err != nil {
			return nil, err
		}
	}
	return
}

func newESGZNode(r esgzreader.Reader, id uint32, base uint32, curPath string) (nodes *NodeLayer, err error) {
	mattr, err := r.Metadata().GetAttr(id)
	if err != nil {
		return nil, err
	}
	attr, qid := metadataToAttr(mattr)
	qid.Path = (uint64(base) << 32) | uint64(id)
	n := &Node{
		qid:  qid,
		attr: attr,
		link: mattr.LinkName,
		r:    io.NewSectionReader(newESGZNodeReaderAt(r, id), 0, int64(attr.Size)),
	}
	var whiteouts []string
	var opaqueWhiteouts []string
	var childErr error
	if err := r.Metadata().ForeachChild(id, func(name string, cid uint32, mode os.FileMode) bool {
		fullname := filepath.Join(curPath, name)
		if strings.HasPrefix(name, whiteoutPrefix) {
			if name == whiteoutOpaqueDir {
				opaqueWhiteouts = append(opaqueWhiteouts, fullname)
			} else {
				whiteouts = append(whiteouts, fullname)
			}
			// Do not add as a node but record as an whiteout
			return true
		}
		cNodes, err := newESGZNode(r, cid, base, fullname)
		whiteouts = append(whiteouts, cNodes.whiteouts...)
		opaqueWhiteouts = append(opaqueWhiteouts, cNodes.opaqueWhiteouts...)
		if err != nil {
			childErr = err
			return false
		}
		if n.children == nil {
			n.children = make(map[string]*Node)
		}
		n.children[name] = cNodes.node
		return true
	}); err != nil {
		return nil, err
	}
	if childErr != nil {
		return nil, err
	}
	return &NodeLayer{node: n, whiteouts: whiteouts, opaqueWhiteouts: opaqueWhiteouts}, nil
}

// mkdev calculates device number bits copmatible to Go
// https://cs.opensource.google/go/x/sys/+/refs/tags/v0.15.0:unix/dev_linux.go;l=36
func mkdev(major, minor uint32) uint64 {
	return ((uint64(major) & 0x00000fff) << 8) |
		((uint64(major) & 0xfffff000) << 32) |
		((uint64(minor) & 0x000000ff) << 0) |
		((uint64(minor) & 0xffffff00) << 12)
}

func metadataToAttr(m esgzmetadata.Attr) (p9.Attr, p9.QID) {
	out := p9.Attr{}
	out.Mode = p9.ModeFromOS(m.Mode)
	out.UID = p9.UID(m.UID)
	out.GID = p9.GID(m.GID)
	out.NLink = 1
	out.RDev = p9.Dev(mkdev(uint32(m.DevMajor), uint32(m.DevMinor)))
	out.Size = uint64(m.Size)
	out.BlockSize = uint64(p9DefaultBlockSize)
	out.Blocks = uint64(m.Size/p9DefaultBlockSize + 1)
	out.ATimeSeconds = uint64(m.ModTime.Unix())
	out.ATimeNanoSeconds = uint64(m.ModTime.UnixNano())
	out.MTimeSeconds = uint64(m.ModTime.Unix())
	out.MTimeNanoSeconds = uint64(m.ModTime.UnixNano())
	out.CTimeSeconds = uint64(m.ModTime.Unix())
	out.CTimeNanoSeconds = uint64(m.ModTime.UnixNano())
	// out.BTimeSeconds     TODO
	// out.BTimeNanoSeconds TODO
	out.Gen = 0
	out.DataVersion = 0

	q := p9.QID{}
	q.Type = p9.ModeFromOS(m.Mode).QIDType()
	// if h.Typeflag == tar.TypeLink {
	// 	q.Type = p9.TypeLink
	// } else {
	// 	q.Type = p9.ModeFromOS(m.Mode).QIDType()
	// }
	return out, q
}

type esgzNodeReaderAt struct {
	r  esgzreader.Reader
	id uint32
	ra io.ReaderAt
	mu sync.Mutex
}

func newESGZNodeReaderAt(r esgzreader.Reader, id uint32) *esgzNodeReaderAt {
	return &esgzNodeReaderAt{r: r, id: id}
}

func (r *esgzNodeReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	if r.ra == nil {
		ra, err := r.r.OpenFile(r.id)
		if err != nil {
			r.mu.Unlock()
			return 0, err
		}
		r.ra = ra
	}
	r.mu.Unlock()
	return r.ra.ReadAt(p, off)
}

func readFileFromNode(n p9.File, p string) (*io.SectionReader, func() error, error) {
	if p == "" {
		_, _, attr, err := n.GetAttr(p9.AttrMask{})
		if err != nil {
			return nil, nil, err
		}
		if _, _, err := n.Open(p9.ReadOnly); err != nil {
			return nil, nil, err
		}
		return io.NewSectionReader(n, 0, int64(attr.Size)), n.Close, nil
	}
	rootdir, remain := rootdirPath(p)
	_, f, err := n.Walk([]string{rootdir})
	if err != nil {
		return nil, nil, err
	}
	return readFileFromNode(f, remain)
}

func rootdirPath(p string) (rootdir string, remain string) {
	d, f := filepath.Split(strings.TrimPrefix(filepath.Clean("/"+p), "/"))
	if d == "" {
		return f, ""
	}
	ro, re := rootdirPath(d)
	return ro, filepath.Join(re, f)
}

type readerAtFunc func([]byte, int64) (int, error)

func (f readerAtFunc) ReadAt(p []byte, offset int64) (int, error) { return f(p, offset) }

type wasmRoundTripper struct{}

func (tr wasmRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return doHttpRoundTrip(req)
}

type layerOCILayoutURLHandler struct {
	addr string
}

func (h *layerOCILayoutURLHandler) Handle(ctx context.Context, desc imagespec.Descriptor) (fetcher esgzremote.Fetcher, size int64, err error) {
	return &layerOCILayoutURLFetcher{newLayerOCILayoutURLReaderAt(h.addr, desc), h, desc}, desc.Size, nil
}

type layerOCILayoutURLFetcher struct {
	// refspec reference.Spec
	r    *io.SectionReader
	h    *layerOCILayoutURLHandler
	desc imagespec.Descriptor
}

func (f *layerOCILayoutURLFetcher) Fetch(ctx context.Context, off int64, size int64) (io.ReadCloser, error) {
	return io.NopCloser(io.NewSectionReader(f.r, off, size)), nil
}

func (f *layerOCILayoutURLFetcher) Check() error {
	return nil
}

func (f *layerOCILayoutURLFetcher) GenID(off int64, size int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d-%d", f.desc.Digest.String(), off, size)))
	return fmt.Sprintf("%x", sum)
}

//go:wasmimport env layer_request
func layer_request(addressP uint32, addresslen uint32, digestP uint32, digestlen uint32, withDecompression uint32, idP uint32) uint32

//go:wasmimport env layer_isreadable
func layer_isreadable(id uint32, isOKP uint32, sizeP uint32) uint32

//go:wasmimport env layer_readat
func layer_readat(id uint32, respP uint32, offset uint32, len uint32, respsizeP uint32) uint32

func newLayerOCILayoutExternalReaderAt(addr string) func(l imagespec.Descriptor, withDecompression bool) (io.ReaderAt, error) {
	return func(l imagespec.Descriptor, withDecompression bool) (io.ReaderAt, error) {
		dgst := l.Digest.Encoded()
		layerAddr := addr + "/blobs/sha256/" + dgst
		var id uint32
		var withDecompressionN uint32 = 0
		if withDecompression {
			withDecompressionN = 1
		}
		res := layer_request(
			uint32(uintptr(unsafe.Pointer(&[]byte(layerAddr)[0]))),
			uint32(len(layerAddr)),
			uint32(uintptr(unsafe.Pointer(&[]byte(dgst)[0]))),
			uint32(len(dgst)),
			uint32(withDecompressionN),
			uint32(uintptr(unsafe.Pointer(&id))),
		)
		if res != 0 {
			return nil, fmt.Errorf("failed to send layer request")
		}
		var isOK uint32 = 0
		var size uint32
		for {
			res := layer_isreadable(id,
				uint32(uintptr(unsafe.Pointer(&isOK))),
				uint32(uintptr(unsafe.Pointer(&size))))
			if res != 0 {
				return nil, fmt.Errorf("layer is not readable")
			}
			if isOK == 1 {
				break
			}
			time.Sleep(1 * time.Millisecond)
		}
		return io.NewSectionReader(readerAtFunc(func(p []byte, offset int64) (retN int, retErr error) {
			var respsize uint32
			res := layer_readat(
				id,
				uint32(uintptr(unsafe.Pointer(&[]byte(p)[0]))),
				uint32(offset), // TODO: FIXME: 64bits
				uint32(len(p)),
				uint32(uintptr(unsafe.Pointer(&respsize))),
			)
			if res != 0 {
				return 0, fmt.Errorf("failed to receive layer response")
			}
			return int(respsize), nil
		}), 0, int64(size)), nil
	}
}

func newLayerOCILayoutURLReaderAt(addr string, l imagespec.Descriptor) *io.SectionReader {
	return io.NewSectionReader(&urlReaderAt{url: addr + "/blobs/sha256/" + l.Digest.Encoded()}, 0, l.Size)
}

type urlReaderAt struct {
	url string
}

func (r *urlReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	c := defaultClient

	req, err := http.NewRequest("GET", r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("unexpected code: %d", resp.StatusCode)
	}
	mt := resp.Header.Get("Content-Type")
	if mt != "" {
		mediaType, _, err := mime.ParseMediaType(mt)
		if err != nil {
			return 0, fmt.Errorf("invalid media type %q: %w", mediaType, err)
		}
		if strings.HasPrefix(mediaType, "multipart/") {
			return 0, fmt.Errorf("multipart unsupported")
		}
	}
	bd, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	copy(p, bd)
	return len(bd), nil
}

func newWaiter() *waiter {
	return &waiter{
		completionCond: sync.NewCond(&sync.Mutex{}),
	}
}

type waiter struct {
	isDone         bool
	isDoneMu       sync.Mutex
	completionCond *sync.Cond
}

func (w *waiter) done() {
	w.isDoneMu.Lock()
	w.isDone = true
	w.isDoneMu.Unlock()
	w.completionCond.Broadcast()
}

func (w *waiter) wait(timeout time.Duration) error {
	wait := func() <-chan struct{} {
		ch := make(chan struct{})
		go func() {
			w.isDoneMu.Lock()
			isDone := w.isDone
			w.isDoneMu.Unlock()

			w.completionCond.L.Lock()
			if !isDone {
				w.completionCond.Wait()
			}
			w.completionCond.L.Unlock()
			ch <- struct{}{}
		}()
		return ch
	}
	select {
	case <-time.After(timeout):
		w.isDoneMu.Lock()
		w.isDone = true
		w.isDoneMu.Unlock()
		w.completionCond.Broadcast()
		return fmt.Errorf("timeout(%v)", timeout)
	case <-wait():
		return nil
	}
}

func NewPositionWatcher(r io.ReaderAt) (*PositionWatcher, error) {
	if r == nil {
		return nil, fmt.Errorf("Target ReaderAt is empty")
	}
	pos := int64(0)
	return &PositionWatcher{r: r, cPos: &pos}, nil
}

type PositionWatcher struct {
	r    io.ReaderAt
	cPos *int64

	mu sync.Mutex
}

func (pwr *PositionWatcher) Read(p []byte) (int, error) {
	pwr.mu.Lock()
	defer pwr.mu.Unlock()

	n, err := pwr.r.ReadAt(p, *pwr.cPos)
	if err == nil {
		*pwr.cPos += int64(n)
	}
	return n, err
}

func (pwr *PositionWatcher) Seek(offset int64, whence int) (int64, error) {
	pwr.mu.Lock()
	defer pwr.mu.Unlock()

	switch whence {
	default:
		return 0, fmt.Errorf("Unknown whence: %v", whence)
	case io.SeekStart:
	case io.SeekCurrent:
		offset += *pwr.cPos
	case io.SeekEnd:
		return 0, fmt.Errorf("Unsupported whence: %v", whence)
	}

	if offset < 0 {
		return 0, fmt.Errorf("invalid offset")
	}
	*pwr.cPos = offset
	return offset, nil
}

func (pwr *PositionWatcher) CurrentPos() int64 {
	pwr.mu.Lock()
	defer pwr.mu.Unlock()

	return *pwr.cPos
}

func headerToAttr(h *tar.Header) (p9.Attr, p9.QID) {
	out := p9.Attr{}
	out.Mode = p9.ModeFromOS(h.FileInfo().Mode())
	out.UID = p9.UID(h.Uid)
	out.GID = p9.GID(h.Gid)
	out.NLink = 1
	out.RDev = p9.Dev(mkdev(uint32(h.Devmajor), uint32(h.Devminor)))
	out.Size = uint64(h.Size)
	out.BlockSize = uint64(p9DefaultBlockSize)
	out.Blocks = uint64(h.Size/p9DefaultBlockSize + 1)
	out.ATimeSeconds = uint64(h.AccessTime.Unix())
	out.ATimeNanoSeconds = uint64(h.AccessTime.UnixNano())
	out.MTimeSeconds = uint64(h.ModTime.Unix())
	out.MTimeNanoSeconds = uint64(h.ModTime.UnixNano())
	out.CTimeSeconds = uint64(h.ChangeTime.Unix())
	out.CTimeNanoSeconds = uint64(h.ChangeTime.UnixNano())
	// out.BTimeSeconds     TODO
	// out.BTimeNanoSeconds TODO
	out.Gen = 0
	out.DataVersion = 0

	q := p9.QID{}
	if h.Typeflag == tar.TypeLink {
		q.Type = p9.TypeLink
	} else {
		q.Type = p9.ModeFromOS(h.FileInfo().Mode()).QIDType()
	}
	return out, q
}

//go:wasmimport env decompress_init
func decompress_init(idP uint32) uint32

//go:wasmimport env decompress_write
func decompress_write(id uint32, bufP uint32, buflen uint32, isEOF uint32) uint32

//go:wasmimport env decompress_read
func decompress_read(id uint32, bufP uint32, buflen uint32, recvLenP uint32, isEOFP uint32) uint32

type wasmDecompressor struct {
	id     uint32
	raw    io.Reader
	rawEOF bool
}

func newWasmDecompressor(r io.Reader) (io.Reader, error) {
	var id uint32
	res := decompress_init(uint32(uintptr(unsafe.Pointer(&id))))
	if res != 0 {
		return nil, fmt.Errorf("failed to init decompressor")
	}
	return &wasmDecompressor{id: id, raw: r}, nil
}

func (d *wasmDecompressor) Read(p []byte) (n int, _ error) {
	if !d.rawEOF {
		n, err := d.raw.Read(p)
		if err != nil && err != io.EOF {
			return n, err
		}
		var isEOF = uint32(0)
		if err == io.EOF {
			isEOF = 1
			d.rawEOF = true
		}
		res := decompress_write(d.id,
			uint32(uintptr(unsafe.Pointer(&[]byte(p)[0]))),
			uint32(n),
			isEOF,
		)
		if res != 0 {
			return 0, fmt.Errorf("failed to write compressed data")
		}
	}
	var recvLen uint32
	var isEOF = uint32(0)
	res := decompress_read(d.id,
		uint32(uintptr(unsafe.Pointer(&[]byte(p)[0]))),
		uint32(len(p)),
		uint32(uintptr(unsafe.Pointer(&recvLen))),
		uint32(uintptr(unsafe.Pointer(&isEOF))),
	)
	if res != 0 {
		return 0, fmt.Errorf("failed to read compressed data")
	}
	var err error
	if isEOF == 1 {
		err = io.EOF
	}
	return int(recvLen), err
}

type gzipDecompressor struct {
	*estargz.GzipDecompressor
}

func newGzipDecompressor() *gzipDecompressor {
	return &gzipDecompressor{&estargz.GzipDecompressor{}}
}

func (gz *gzipDecompressor) Reader(r io.Reader) (io.ReadCloser, error) {
	zr, err := newWasmDecompressor(r)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(zr), nil
}

type readerWithCloser struct {
	r         io.Reader
	closeFunc func() error
}

func (r *readerWithCloser) Read(p []byte) (int, error) {
	return r.r.Read(p)
}

func (r *readerWithCloser) Close() error {
	return r.closeFunc()
}
