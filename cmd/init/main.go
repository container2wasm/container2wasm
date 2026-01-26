package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	inittype "github.com/ktock/container2wasm/cmd/init/types"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	// initConfigPath is path to the config file used by this init process.
	initConfigPath = "/oci/initconfig.json"

	// wasi0: wasi root directory
	rootFSTag = "wasi0"
	// wasi1: pack directory
	packFSTag = "wasi1"

	// wasi2ConfigPath is the config file path for wasip2 mode
	wasi2ConfigPath = "/pack/wasi2-config"
)

// isWasiP2Mode returns true if running in WASI Preview 2 mode.
// Detection is file-based: the wasi2-config file only exists in wasip2 builds.
// We check for this file rather than an environment variable because the init
// process runs inside the Linux VM (Bochs/QEMU), where os.Getenv reads from
// the VM's environment, not the WASI runtime's environment.
func isWasiP2Mode() bool {
	_, err := os.Stat(wasi2ConfigPath)
	return err == nil
}

func main() {
	if err := doInit(); err != nil {
		panic(err)
	}
}

func doInit() error {
	os.Setenv("PATH", "/bin:/sbin:/usr/bin:/usr/sbin:/usr/local/bin")
	os.Setenv("HOME", "/root")
	os.Setenv("TERM", "vt100")

	cfgD, err := os.ReadFile(initConfigPath)
	if err != nil {
		return fmt.Errorf("cannot read boot config: %w", err)
	}
	var cfg inittype.BootConfig
	if err := json.Unmarshal(cfgD, &cfg); err != nil {
		return fmt.Errorf("cannot parse boot config: %w", err)
	}
	scmd := exec.Command("stty", "-echo")
	scmd.Stdin = os.Stdin
	if err := scmd.Run(); err != nil {
		return fmt.Errorf("failed to disable tty echo: %w", err)
	}
	if cfg.Debug || cfg.DebugInit {
		log.SetOutput(os.Stdout)
	} else {
		log.SetOutput(io.Discard)
	}
	var imageConfig imagespec.Image
	var externalBundle bool
	if cfg.Container.ExternalBundle {
		externalBundle = true
	} else {
		imageD, err := os.ReadFile(cfg.Container.ImageConfigPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(imageD, &imageConfig); err != nil {
			return err
		}
	}

	if err := mountAll(cfg.Mounts); err != nil {
		return err
	}

	var s runtimespec.Spec
	if !externalBundle {
		specD, err := os.ReadFile(cfg.Container.RuntimeConfigPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(specD, &s); err != nil {
			return err
		}
	}
	for _, cmd := range cfg.CmdPreRun {
		log.Printf("executing(pre-run): %+v\n", cmd)
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Stdout = log.Writer()
		c.Stderr = log.Writer()
		if err := c.Run(); err != nil {
			return fmt.Errorf("failed to pre-run %v: %w", cmd, err)
		}
	}

	if cfg.Debug {
		log.SetOutput(os.Stdout)
	} else {
		log.SetOutput(io.Discard)
	}

	var info runtimeFlags
	if os.Getenv("NO_RUNTIME_CONFIG") != "1" && os.Getenv("QEMU_MODE") != "1" {
		if isWasiP2Mode() {
			// WASI P2 mode: use standard WASI interfaces
			log.Println("Running in WASI P2 mode")

			// Sync VM clock to host via wasi:clocks/wall-clock
			if err := syncVMClock(); err != nil {
				log.Printf("Warning: %v", err)
			}

			// Get basic config from wasi:cli (args, env)
			info = getRuntimeFlagsFromCLI()

			// Read advanced config from embedded file if present
			if configD, err := os.ReadFile(wasi2ConfigPath); err == nil {
				log.Printf("WASI2 CONFIG:\n%s\n", string(configD))
				advancedInfo := parseWasi2Config(configD)
				// Merge advanced config into info
				info.mounts = append(info.mounts, advancedInfo.mounts...)
				info.withNet = advancedInfo.withNet
				info.mac = advancedInfo.mac
				info.bundle = advancedInfo.bundle
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("failed to read wasi2 config: %w", err)
			}
			// If no config file and no CLI args, that's OK - use image defaults
		} else {
			// Legacy WASI P1 mode: wait for host signal and read info file
			// WASI-related filesystems
			for _, tag := range []string{rootFSTag, packFSTag} {
				dst := filepath.Join("/mnt", tag)
				if err := os.Mkdir(dst, 0777); err != nil {
					return err
				}
				log.Printf("mounting %q to %q\n", tag, dst)
				if err := syscall.Mount(tag, dst, "9p", 0, "trans=virtio,version=9p2000.L,msize=8192"); err != nil {
					log.Printf("failed mounting %q: %v\n", tag, err)
					break
				}
			}

			// Wizer snapshot can be created by the host here
			//////////////////////////////////////////////////////////////////////
			fmt.Printf("==========") // special string not printed
			var b [2]byte
			var bPos int
			bTargetPos := 1
			for {
				if _, err := os.Stdin.Read(b[:]); err != nil {
					return err
				}
				log.Printf("HOST: got %q\n", string(b[:]))
				if b[0] == '=' && b[1] == '\n' {
					bPos++
					if bPos == bTargetPos {
						break
					}
					continue
				}
				bPos = 0
			}
			///////////////////////////////////////////////////////////////////////

			infoD, err := os.ReadFile(filepath.Join("/mnt", packFSTag, "info"))
			if err != nil {
				return err
			}
			log.Printf("INFO:\n%s\n", string(infoD))
			info = parseInfo(infoD)
		}
	}

	if os.Getenv("NO_RUNTIME_CONFIG") != "1" && os.Getenv("QEMU_MODE") == "1" {
		packFSDst := filepath.Join("/mnt", packFSTag)
		if err := os.Mkdir(packFSDst, 0777); err != nil {
			return err
		}
		// QEMU snapshot can be created here
		//////////////////////////////////////////////////////////////////////
		fmt.Printf("==========") // special string not printed
		for {
			time.Sleep(time.Second) // expect a snapshot is taken
			if err := syscall.Mount(packFSTag, packFSDst, "9p", 0, "trans=virtio,version=9p2000.L"); err != nil {
				//return fmt.Errorf("failed mounting(pack) %q: %w", packFSTag, err)
				continue
			}
			if _, err := os.Stat(filepath.Join(packFSDst, "info")); err == nil {
				break // info file exists
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to stat info file: %w", err)
			}
			if err := syscall.Unmount(packFSDst, 0); err != nil {
				return fmt.Errorf("failed unmounting(pack) %q: %w", packFSTag, err)
			}
		}
		///////////////////////////////////////////////////////////////////////

		// WASI-related filesystems
		for _, tag := range []string{rootFSTag} {
			dst := filepath.Join("/mnt", tag)
			if err := os.Mkdir(dst, 0777); err != nil {
				return err
			}
			log.Printf("mounting %q to %q\n", tag, dst)
			if err := syscall.Mount(tag, dst, "9p", 0, "trans=virtio,version=9p2000.L"); err != nil {
				log.Printf("failed mounting %q: %v\n", tag, err)
				break
			}
		}

		infoD, err := os.ReadFile(filepath.Join("/mnt", packFSTag, "info"))
		if err != nil {
			return err
		}
		log.Printf("INFO:\n%s\n", string(infoD))
		info = parseInfo(infoD)
	}

	if info.withNet {
		if info.mac != "" {
			if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "down").CombinedOutput(); err != nil {
				return fmt.Errorf("failed eth0 down: %v: %w", string(o), err)
			}
			if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "address", info.mac).CombinedOutput(); err != nil {
				return fmt.Errorf("failed change mac address of eth0: %v: %w", string(o), err)
			}
		}
		if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "up").CombinedOutput(); err != nil {
			return fmt.Errorf("failed eth0 up: %v: %w", string(o), err)
		}
		if o, err := exec.Command("udhcpc", "-i", "eth0").CombinedOutput(); err != nil {
			return fmt.Errorf("failed udhcpc: %w", err)
		} else if cfg.Debug {
			o2, _ := exec.Command("ip", "a").CombinedOutput()
			log.Printf("finished udhcpc: %s\n %s\n", string(o), string(o2))
		}
	}

	if externalBundle {
		if info.bundle == "" {
			return fmt.Errorf("neither of embedded image nor external bundle is provided")
		}
		if !info.withNet {
			return fmt.Errorf("networking must be enabled")
		}

		// mount bundle
		addr, ok := strings.CutPrefix(info.bundle, "9p=")
		if !ok {
			return fmt.Errorf("unsupported external bundle %q", info.bundle)
		}

		bundle9pPath := "/run/9pbundle"
		bundle9pSpecPath := filepath.Join(bundle9pPath, "config", "config.json")
		bundle9pImageConfigPath := filepath.Join(bundle9pPath, "config", "imageconfig.json")

		if err := os.MkdirAll(bundle9pPath, os.FileMode(0755)); err != nil {
			return fmt.Errorf("failed to create %q: %w", bundle9pPath, err)
		}
		if err := syscall.Mount(addr, bundle9pPath, "9p", 0, "trans=tcp,version=9p2000.L,msize=5000000,port=80,cache=loose,ro"); err != nil {
			return fmt.Errorf("failed mounting oci %v", err)
		}

		// make bundle usable
		// TODO: ovrelay mount /run/bundle/rootfs directly to /run/rootfs
		if cfg.Container.ImageRootfsPath == "" {
			return fmt.Errorf("specify image rootfs path")
		}
		if err := os.MkdirAll(cfg.Container.ImageRootfsPath, os.FileMode(0755)); err != nil {
			return fmt.Errorf("failed to create %q: %w", cfg.Container.ImageRootfsPath, err)
		}
		if err := syscall.Mount(filepath.Join(bundle9pPath, "rootfs"), cfg.Container.ImageRootfsPath, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("cannot bind mount 9p rootfs to %q: %w", cfg.Container.ImageRootfsPath, err)
		}

		// parse config
		f, err := os.Open(bundle9pSpecPath)
		if err != nil {
			return fmt.Errorf("failed to open spec file: %v", err)
		}
		if err := json.NewDecoder(f).Decode(&s); err != nil {
			return err
		}
		f.Close()
		f, err = os.Open(bundle9pImageConfigPath)
		if err != nil {
			return fmt.Errorf("failed to open image config file: %v", err)
		}
		if err := json.NewDecoder(f).Decode(&imageConfig); err != nil {
			return err
		}
		f.Close()
	}

	if err := mountAll(cfg.PostMounts); err != nil {
		return err
	}

	s = patchSpec(s, info, imageConfig)
	log.Printf("Running: %+v\n", s.Process.Args)
	sd, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.Container.BundlePath, "config.json"), sd, 0600); err != nil {
		return err
	}
	if info.withNet {
		for _, f := range []string{"/etc/hosts", "/etc/resolv.conf"} {
			if err := syscall.Mount(f, filepath.Join("/run/rootfs", f), "", syscall.MS_BIND, ""); err != nil {
				return fmt.Errorf("cannot mount %q: %w", f, err)
			}
		}
	}

	var lastErr error
	for _, cmd := range cfg.Cmd {
		log.Printf("executing: %+v\n", cmd)
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		// TODO: signal?
		if err := c.Run(); err != nil {
			lastErr = fmt.Errorf("failed to run %v: %w", cmd, err)
			break
		}
	}

	if err := exec.Command("poweroff", "-f").Run(); err != nil {
		return fmt.Errorf("failed running poweroff")
	}
	return lastErr
}

func mount(m inittype.MountInfo) error {
	log.Printf("mounting %+v\n", m)
	for _, d := range m.Dir {
		if err := os.MkdirAll(d.Path, os.FileMode(d.Mode)); err != nil {
			return fmt.Errorf("failed to create %q: %w", d.Path, err)
		}
	}
	for _, f := range m.File {
		cf, err := os.Create(f.Path)
		if err != nil {
			return fmt.Errorf("failed to create %q: %w", f.Path, err)
		}
		if _, err := cf.Write([]byte(f.Contents)); err != nil {
			return fmt.Errorf("failed to write contents to %q: %w", f.Path, err)
		}
		if err := cf.Close(); err != nil {
			return fmt.Errorf("failed to close %q: %w", f.Path, err)
		}
	}
	if err := syscall.Mount(m.Src, m.Dst, m.FSType, m.Flags, m.Data); err != nil {
		return fmt.Errorf("cannot mount %q %q %q: %w", m.Src, m.Dst, m.FSType, err)
	}
	if len(m.Cmd) > 0 {
		log.Println(m.Cmd)
		c := exec.Command(m.Cmd[0], m.Cmd[1:]...)
		c.Stdout = log.Writer()
		c.Stderr = log.Writer()
		// TODO: signal?
		if err := c.Run(); err != nil {
			return fmt.Errorf("failed to run command %+v: %w", m.Cmd, err)
		}
	}
	for _, d := range m.PostDir {
		if err := os.MkdirAll(d.Path, os.FileMode(d.Mode)); err != nil {
			return fmt.Errorf("failed to create %q: %w", d.Path, err)
		}
	}
	for _, f := range m.PostFile {
		cf, err := os.Create(f.Path)
		if err != nil {
			return fmt.Errorf("failed to create %q: %w", f.Path, err)
		}
		if _, err := cf.Write([]byte(f.Contents)); err != nil {
			return fmt.Errorf("failed to write contents to %q: %w", f.Path, err)
		}
		if err := cf.Close(); err != nil {
			return fmt.Errorf("failed to close %q: %w", f.Path, err)
		}
	}
	return nil
}

func mountAll(mounts []inittype.MountInfo) error {
	if len(mounts) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	for _, m := range mounts {
		if m.Async {
			m := m
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := mount(m); err != nil {
					if m.Optional {
						log.Printf("failed optional mount %+v: %v", m, err)
					} else {
						panic(err)
					}
				}
			}()
		} else {
			if err := mount(m); err != nil {
				if m.Optional {
					log.Printf("failed optional mount %+v: %v", m, err)
				} else {
					return err
				}
			}
		}
	}

	wg.Wait()

	return nil
}

var (
	delimLines = regexp.MustCompile(`[^\\]\n`)
	delimArgs  = regexp.MustCompile(`[^\\] `)
)

type runtimeFlags struct {
	mounts     []runtimespec.Mount
	env        []string
	entrypoint []string
	args       []string

	withNet bool
	mac     string
	bundle  string
}

func parseInfo(infoD []byte) (info runtimeFlags) {
	var options []string
	lmchs := delimLines.FindAllIndex(infoD, -1)
	prev := 0
	for _, m := range lmchs {
		s := m[0] + 1
		// newline are quoted so we restore them here
		options = append(options, strings.ReplaceAll(string(infoD[prev:s]), "\\\n", "\n"))
		prev = m[1]
	}
	options = append(options, strings.ReplaceAll(string(infoD[prev:]), "\\\n", "\n"))
	for _, l := range options {
		elms := strings.SplitN(l, ":", 2)
		if len(elms) != 2 {
			continue
		}
		inst := elms[0]
		o := strings.TrimLeft(elms[1], " ")
		switch inst {
		case "m":
			if o == "" {
				// no path is specified; nop
				continue
			}
			info.mounts = append(info.mounts, runtimespec.Mount{
				Type:        "bind",
				Source:      filepath.Join("/mnt/wasi0", o),
				Destination: filepath.Join("/", o), // TODO: ensure not outside of "/"
				Options:     []string{"bind"},
			})
			log.Printf("Prepared mount wasi0 => %q", o)
		case "c":
			info.args = nil
			mchs := delimArgs.FindAllIndex([]byte(o), -1)
			prev := 0
			for _, m := range mchs {
				s := m[0] + 1
				// spaces are quoted so we restore them here
				info.args = append(info.args, strings.ReplaceAll(o[prev:s], "\\ ", " "))
				prev = m[1]
			}
			info.args = append(info.args, strings.ReplaceAll(o[prev:], "\\ ", " "))
		case "e":
			info.entrypoint = []string{o}
		case "env":
			info.env = append(info.env, o)
		case "n":
			info.withNet = true // TODO: check mode (e.g. dhcp, ...)
			info.mac = o
		case "t":
			if o != "" {
				if err := exec.Command("date", "+%s", "-s", "@"+o).Run(); err != nil {
					log.Printf("failed setting date: %v", err) // TODO: return error
				}
			}
		case "b":
			info.bundle = o
		default:
			log.Printf("unsupported prefix: %q", inst)
		}
	}
	return
}

// parseWasi2Config parses the wasip2-specific config file.
// Only handles features not covered by WASI standard interfaces:
// - m: bind mounts (no WASI equivalent)
// - n: networking (no WASI equivalent)
// - b: external bundle (no WASI equivalent)
//
// Note: args/env come from wasi:cli, timestamp auto-syncs via wasi:clocks
func parseWasi2Config(configD []byte) (info runtimeFlags) {
	var options []string
	lmchs := delimLines.FindAllIndex(configD, -1)
	prev := 0
	for _, m := range lmchs {
		s := m[0] + 1
		options = append(options, strings.ReplaceAll(string(configD[prev:s]), "\\\n", "\n"))
		prev = m[1]
	}
	options = append(options, strings.ReplaceAll(string(configD[prev:]), "\\\n", "\n"))

	for _, l := range options {
		elms := strings.SplitN(l, ":", 2)
		if len(elms) != 2 {
			continue
		}
		inst := elms[0]
		o := strings.TrimLeft(elms[1], " ")
		switch inst {
		case "m":
			if o == "" {
				continue
			}
			info.mounts = append(info.mounts, runtimespec.Mount{
				Type:        "bind",
				Source:      filepath.Join("/mnt/wasi0", o),
				Destination: filepath.Join("/", o),
				Options:     []string{"bind"},
			})
			log.Printf("Prepared mount wasi0 => %q", o)
		case "n":
			info.withNet = true
			info.mac = o
		case "b":
			info.bundle = o
		default:
			// Ignore directives handled by WASI standard interfaces:
			// - c:, e:, env: come from wasi:cli
			// - t: auto-syncs via wasi:clocks
			if inst != "c" && inst != "e" && inst != "env" && inst != "t" {
				log.Printf("unsupported wasi2-config directive: %q", inst)
			}
		}
	}
	return
}

// getRuntimeFlagsFromCLI builds runtime flags from wasi:cli interfaces.
// In wasip2 mode, args and env come from the WASI runtime, not a config file.
func getRuntimeFlagsFromCLI() runtimeFlags {
	var info runtimeFlags

	// Get args from os.Args (maps to wasi:cli/environment.get-arguments)
	// Convention: first arg after program name is entrypoint, rest are args
	// Example: wasmtime run out.wasm -- /bin/sh -c "echo hello"
	//          os.Args = ["/bin/sh", "-c", "echo hello"]
	args := os.Args[1:] // Skip program name (the wasm itself)
	if len(args) > 0 {
		info.entrypoint = []string{args[0]}
		if len(args) > 1 {
			info.args = args[1:]
		}
	}

	// Get env from os.Environ (maps to wasi:cli/environment.get-environment)
	// Filter out internal vars
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "WASI_") {
			continue // Skip WASI internal vars
		}
		info.env = append(info.env, e)
	}

	return info
}

// syncVMClock sets the VM's internal clock to match the host's wall clock.
// In wasip2 mode, time.Now() reads from wasi:clocks/wall-clock, giving us
// the host's current time which we then set inside the VM.
func syncVMClock() error {
	hostTime := time.Now().Unix()
	if err := exec.Command("date", "+%s", "-s", fmt.Sprintf("@%d", hostTime)).Run(); err != nil {
		return fmt.Errorf("failed to sync VM clock: %w", err)
	}
	log.Printf("Synced VM clock to host time: %d", hostTime)
	return nil
}

func patchSpec(s runtimespec.Spec, info runtimeFlags, imageConfig imagespec.Image) runtimespec.Spec {
	s.Mounts = append(s.Mounts, info.mounts...)
	s.Process.Env = append(s.Process.Env, info.env...)
	entrypoint := info.entrypoint
	if len(entrypoint) == 0 {
		entrypoint = imageConfig.Config.Entrypoint
	}
	args := info.args
	if len(args) == 0 {
		args = imageConfig.Config.Cmd
	}
	s.Process.Args = append(entrypoint, args...)
	return s
}
