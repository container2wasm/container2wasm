package main

import (
	"encoding/json"
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
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("panic:", r)
		}
		if err := exec.Command("poweroff", "-f").Run(); err != nil {
			panic("failed running poweroff")
		}
	}()

	if err := doInit(); err != nil {
		panic(err)
	}
}

func fsExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
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
	info := parseInfo(infoD)

	if info.withNet {
		if info.mac != "" {
			log.Printf("Setting up MAC:\n%s\n", info.mac)
			if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "down").CombinedOutput(); err != nil {
				return fmt.Errorf("failed eth0 down: %v: %w", string(o), err)
			}
			if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "address", info.mac).CombinedOutput(); err != nil {
				return fmt.Errorf("failed change mac address of eth0: %v: %w", string(o), err)
			}
		}
		log.Println("Ethernet UP...")
		if o, err := exec.Command("ip", "link", "set", "dev", "eth0", "up").CombinedOutput(); err != nil {
			return fmt.Errorf("failed eth0 up: %v: %w", string(o), err)
		}

		log.Println("DHCP...")
		if o, err := exec.Command("udhcpc", "-i", "eth0").CombinedOutput(); err != nil {
			return fmt.Errorf("failed udhcpc: %w", err)
		} else if cfg.Debug {
			o2, _ := exec.Command("ip", "a").CombinedOutput()
			log.Printf("finished udhcpc: %s\n %s\n", string(o), string(o2))
		}
	}

	if externalBundle {
		bundlePath := "/mnt/wasi0/ext/bundle"
		bindRootFS := false

		if strings.HasPrefix(info.bundle, "9p=") {
			if !info.withNet {
				return fmt.Errorf("networking must be enabled for 9p")
			}

			// mount 9p bundle
			addr, ok := strings.CutPrefix(info.bundle, "9p=")
			if !ok {
				return fmt.Errorf("unsupported external bundle %q", info.bundle)
			}

			if err := os.MkdirAll(bundlePath, os.FileMode(0755)); err != nil {
				return fmt.Errorf("failed to create %q: %w", bundlePath, err)
			}
			if err := syscall.Mount(addr, bundlePath, "9p", 0, "trans=tcp,version=9p2000.L,msize=5000000,port=80,cache=loose,ro"); err != nil {
				return fmt.Errorf("failed mounting oci %v", err)
			}

			bindRootFS = true

		} else {
			if info.bundle != "" {
				if info.bundle[0] != '/' {
					return fmt.Errorf("invalid external bundle path")
				}
				bundlePath = filepath.Join("/mnt/wasi0", info.bundle)
			}
			if !fsExists(bundlePath) {
				return fmt.Errorf("can't find external bundle in %s", bundlePath)
			}

		}

		bundleSpecPath := filepath.Join(bundlePath, "config", "config.json")
		bundleImageConfigPath := filepath.Join(bundlePath, "config", "imageconfig.json")

		if !fsExists(bundleSpecPath) || !fsExists(bundleImageConfigPath) {
			return fmt.Errorf("invalid external bundle format")
		}

		// make bundle usable
		// TODO: ovrelay mount /run/bundle/rootfs directly to /run/rootfs
		if cfg.Container.ImageRootfsPath == "" {
			return fmt.Errorf("specify image rootfs path")
		}
		if err := os.MkdirAll(cfg.Container.ImageRootfsPath, os.FileMode(0755)); err != nil {
			return fmt.Errorf("failed to create %q: %w", cfg.Container.ImageRootfsPath, err)
		}

		if bindRootFS {
			if err := syscall.Mount(filepath.Join(bundlePath, "rootfs"), cfg.Container.ImageRootfsPath, "", syscall.MS_BIND, ""); err != nil {
				return fmt.Errorf("cannot bind mount 9p rootfs to %q: %w", cfg.Container.ImageRootfsPath, err)
			}
		} else {
			var err error
			for _, fsType := range []string{"erofs", "squashfs", "iso9660"} {
				cmd := exec.Command("/bin/mount", "-t", fsType, "-o", "loop", filepath.Join(bundlePath, "rootfs.bin"), cfg.Container.ImageRootfsPath)
				if _, err = cmd.Output(); err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("failed mounting oci with %s", string(err.(*exec.ExitError).Stderr))
			}
		}

		// parse config
		f, err := os.Open(bundleSpecPath)
		if err != nil {
			return fmt.Errorf("failed to open spec file: %v", err)
		}
		if err := json.NewDecoder(f).Decode(&s); err != nil {
			return err
		}
		f.Close()
		f, err = os.Open(bundleImageConfigPath)
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

	info.bundle = "/ext/bundle"
	mounts := make(map[string]runtimespec.Mount)

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
			mounts[o] = runtimespec.Mount{
				Type:        "bind",
				Source:      filepath.Join("/mnt/wasi0", o),
				Destination: filepath.Join("/", o), // TODO: ensure not outside of "/"
				Options:     []string{"bind"},
			}
			log.Printf("Prepared mount wasi0 => %q", o)
		case "c":
			info.args = nil
			mchs := delimArgs.FindAllIndex([]byte(o), -1)
			prev := 0
			for _, m := range mchs {
				s := m[0] + 1
				// spaces are quoted so we restore them here
				info.args = append(info.args, strings.ReplaceAll(string(o[prev:s]), "\\ ", " "))
				prev = m[1]
			}
			info.args = append(info.args, strings.ReplaceAll(string(o[prev:]), "\\ ", " "))
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

	for o, m := range mounts {
		if o != info.bundle {
			info.mounts = append(info.mounts, m)
		}
	}

	return
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
