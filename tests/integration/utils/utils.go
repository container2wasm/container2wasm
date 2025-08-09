package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// Defined in Dockerfile.test.
// TODO: Make it a flag
const AssetPath = "/test/"
const C2wBin = "c2w"
const C2wNetProxyBin = "/opt/c2w-net-proxy.wasm"
const ImageMounterBin = "/opt/imagemounter.wasm"

type Architecture int

const (
	X8664 Architecture = iota
	RISCV64
	AArch64
)

func archToString(t *testing.T, a Architecture) string {
	switch a {
	case X8664:
		return "x86_64"
	case RISCV64:
		return "riscv64"
	case AArch64:
		return "aarch64"
	default:
		t.Fatalf("unknown architecture %d", a)
		return ""
	}
}

type Input struct {
	Image        string
	ConvertOpts  []string
	Architecture Architecture
	Dockerfile   string
	BuildArgs    []string
	Mirror       bool
	Store        string
	External     bool
}

type Env struct {
	Input   Input
	Workdir string
}

type TestSpec struct {
	Name           string
	Inputs         []Input
	RuntimeEnv     [][]string
	Prepare        func(t *testing.T, env Env)
	Finalize       func(t *testing.T, env Env)
	ImageName      string // default: test.wasm
	Runtime        string
	RuntimeOpts    func(t *testing.T, env Env) []string
	Args           func(t *testing.T, env Env) []string
	Want           func(t *testing.T, env Env, in io.Writer, out io.Reader)
	NoParallel     bool
	IgnoreExitCode bool
	ToJS           bool
	KillRuntime    bool
}

func RunTestRuntimes(t *testing.T, tests ...TestSpec) {
	for _, tt := range tests {
		tt := tt
		for _, in := range tt.Inputs {
			in := in
			runtimeEnv := tt.RuntimeEnv
			if len(runtimeEnv) == 0 {
				runtimeEnv = [][]string{make([]string, 0)}
			}
			for _, e := range runtimeEnv {
				runTest(t, tt, in, e)
			}
		}
	}
}

func runTest(t *testing.T, tt TestSpec, in Input, runtimeEnv []string) {
	t.Run(strings.ReplaceAll(strings.Join(append(append([]string{tt.Name, in.Image, fmt.Sprintf("arch=%s", archToString(t, in.Architecture))}, in.ConvertOpts...), runtimeEnv...), ","), "/", "-"), func(t *testing.T) {
		if !tt.NoParallel {
			t.Parallel()
		}

		tmpdir, err := os.MkdirTemp("", "testc2w")
		assert.NilError(t, err)
		t.Logf("test root: %v", tmpdir)
		defer func() {
			assert.NilError(t, os.RemoveAll(tmpdir))
		}()

		if in.Dockerfile != "" {
			df := filepath.Join(tmpdir, "Dockerfile-integrationtest")
			assert.NilError(t, os.WriteFile(df, []byte(in.Dockerfile), 0755))
			dcmd := exec.Command("docker", append([]string{"build", "--progress=plain", "-t", in.Image, "-f", df, AssetPath}, in.BuildArgs...)...)
			dcmd.Stdout = os.Stdout
			dcmd.Stderr = os.Stderr
			assert.NilError(t, dcmd.Run())
		}
		if in.Mirror {
			if err := exec.Command("docker", "image", "inspect", in.Image).Run(); err != nil {
				assert.NilError(t, exec.Command("docker", "pull", in.Image).Run())
			}
			assert.NilError(t, exec.Command("docker", "tag", in.Image, "localhost:5000/"+in.Image).Run())
			dcmd := exec.Command("docker", "push", "localhost:5000/"+in.Image)
			dcmd.Stdout = os.Stdout
			dcmd.Stderr = os.Stderr
			assert.NilError(t, dcmd.Run())
		}
		if in.Store != "" {
			waitForBuildxBuilder(t, "container")
			if err := exec.Command("docker", "image", "inspect", in.Image).Run(); err != nil {
				assert.NilError(t, exec.Command("docker", "pull", in.Image).Run())
			}
			df := filepath.Join(tmpdir, "Dockerfile-integrationtest-store")
			tmpdest := filepath.Join(tmpdir, "Dockerfile-integrationtest-store-out.tar")
			assert.NilError(t, os.WriteFile(df, []byte("FROM "+in.Image), 0755))
			dcmd := exec.Command("docker", "buildx", "build", "--builder=container", "--output", "type=oci,dest="+tmpdest,  "-f", df, AssetPath)
			dcmd.Stdout = os.Stdout
			dcmd.Stderr = os.Stderr
			assert.NilError(t, dcmd.Run())

			storeout := filepath.Join(tmpdir, in.Store)
			assert.NilError(t, os.Mkdir(storeout, 0755))
			assert.NilError(t, exec.Command("tar", "-C", storeout, "-xf", tmpdest).Run())
		}

		var convertargs []string
		var dst string
		if tt.ToJS {
			// TODO: check /htdocs/ existence
			convertargs = append(convertargs, "--to-js")
			dst = "/htdocs/"
		} else {
			dst = filepath.Join(tmpdir, "test.wasm")
		}
		if in.Image != "" && !in.External {
			convertargs = append(convertargs, in.Image, dst)
		} else {
			convertargs = append(convertargs, dst)
		}
		c2wCmd := exec.Command(C2wBin, append(append(in.ConvertOpts, "--assets="+AssetPath), convertargs...)...)
		c2wCmd.Stdout = os.Stdout
		c2wCmd.Stderr = os.Stderr
		assert.NilError(t, c2wCmd.Run())

		envInfo := Env{Input: in, Workdir: tmpdir}
		if tt.Prepare != nil {
			tt.Prepare(t, envInfo)
		}
		if tt.Finalize != nil {
			defer tt.Finalize(t, envInfo)
		}

		targetWasm := dst
		if tt.ImageName != "" {
			targetWasm = filepath.Join(tmpdir, tt.ImageName)
		}
		var runtimeOpts []string
		if tt.RuntimeOpts != nil {
			runtimeOpts = tt.RuntimeOpts(t, envInfo)
		}
		if !tt.ToJS {
			runtimeOpts = append(runtimeOpts, targetWasm)
		}
		var args []string
		if tt.Args != nil {
			args = tt.Args(t, envInfo)
		}
		testCmd := exec.Command(tt.Runtime, append(runtimeOpts, args...)...)
		if runtimeEnv != nil {
			testCmd.Env = append(testCmd.Environ(), runtimeEnv...)
		}
		outR, err := testCmd.StdoutPipe()
		assert.NilError(t, err)
		defer outR.Close()
		inW, err := testCmd.StdinPipe()
		assert.NilError(t, err)
		defer inW.Close()
		testCmd.Stderr = os.Stderr

		assert.NilError(t, testCmd.Start())

		time.Sleep(3 * time.Second) // wait for container fully up-and-running. TODO: introduce synchronization

		rr := io.TeeReader(outR, os.Stdout)
		tt.Want(t, envInfo, inW, rr)
		inW.Close()

		if tt.KillRuntime {
			testCmd.Process.Signal(os.Interrupt)
			io.Copy(io.Discard, rr)
		}

		if !tt.IgnoreExitCode {
			assert.NilError(t, testCmd.Wait())
		} else {
			if err := testCmd.Wait(); err != nil {
				t.Logf("command test error: %v", err)
			}
		}

		// cleanup cache
		assert.NilError(t, exec.Command("docker", "buildx", "prune", "-f", "--keep-storage=10GB").Run())
		assert.NilError(t, exec.Command("docker", "system", "prune").Run())
	})
}

func waitForBuildxBuilder(t *testing.T, builder string) {
	tmpdir, err := os.MkdirTemp("", "testc2w")
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, os.RemoveAll(tmpdir))
	}()
	df := filepath.Join(tmpdir, "buildxwaiter")
	assert.NilError(t, os.WriteFile(df, []byte("FROM ubuntu:22.04"), 0755))
	for i := 0; i < 10; i++ {
		dcmd := exec.Command("docker", "buildx", "build", "--builder="+builder, "--progress=plain", "-f", df, tmpdir)
		dcmd.Stdout = os.Stdout
		dcmd.Stderr = os.Stderr
		if err := dcmd.Run(); err != nil {
			t.Logf("failed to access to buildkit: %v", err)
			time.Sleep(time.Second)
			continue
		}
		t.Logf("builder working")
		return
	}
	t.Fatalf("failed to launch builder (timeout)")
}

func WantString(wantstr string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return func(t *testing.T, env Env, in io.Writer, out io.Reader) {
		outstr, err := io.ReadAll(out)
		assert.NilError(t, err)
		assert.Equal(t, string(outstr), wantstr)
	}
}

func wantPrompt(withExit bool, contains bool, prompt string, inputoutput ...[2]string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return func(t *testing.T, env Env, in io.Writer, out io.Reader) {
		ctx := context.TODO()

		// Wait for prompt
		_, err := readUntilPrompt(ctx, prompt, out)
		assert.NilError(t, err)

		// Wait for prompt is functional
		promptCh := make(chan struct{})
		go func() {
			_, err = readUntilPrompt(ctx, prompt, out)
			assert.NilError(t, err)
			close(promptCh)
		}()
		i := 0
	LOOP:
		for {
			_, err = in.Write([]byte("\n"))
			assert.NilError(t, err)
			select {
			case <-promptCh:
				break LOOP
			case <-time.After(time.Second):
				t.Logf("prompt is not functional, retrying...(%d)", i)
			}
			i++
		}
		t.Logf("prompt is functional")

		// Disable echo back
		_, err = in.Write([]byte("stty -echo\n"))
		assert.NilError(t, err)
		_, err = readUntilPrompt(ctx, prompt, out)
		assert.NilError(t, err)

		// Test IO
		for _, iop := range inputoutput {
			input, output := iop[0], iop[1]
			_, err := in.Write([]byte(input))
			assert.NilError(t, err)
			outstr, err := readUntilPrompt(ctx, prompt, out)
			assert.NilError(t, err)
			if contains {
				assert.Equal(t, strings.Contains(string(outstr), output), true)
			} else {
				assert.Equal(t, string(outstr), output)
			}
		}

		if withExit {
			// exit the container
			_, err = in.Write([]byte("exit\n"))
			assert.NilError(t, err)
			_, err = io.ReadAll(out)
			assert.NilError(t, err)
		}
	}
}

func WantPrompt(prompt string, inputoutput ...[2]string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return wantPrompt(true, false, prompt, inputoutput...)
}

func WantPromptWithoutExit(prompt string, inputoutput ...[2]string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return wantPrompt(false, false, prompt, inputoutput...)
}

func ContainsPromptWithoutExit(prompt string, inputoutput ...[2]string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return wantPrompt(false, true, prompt, inputoutput...)
}

func WantPromptWithWorkdir(prompt string, inputoutputFunc func(workdir string) [][2]string) func(t *testing.T, env Env, in io.Writer, out io.Reader) {
	return func(t *testing.T, env Env, in io.Writer, out io.Reader) {
		WantPrompt(prompt, inputoutputFunc(env.Workdir)...)(t, env, in, out)
	}
}

func readUntilPrompt(ctx context.Context, prompt string, outR io.Reader) (out []byte, retErr error) {
	var buf [1]byte
	for {
		_, err := outR.Read(buf[:])
		if err != nil {
			return out, err
		}
		out = append(out, buf[0])
		if i := strings.LastIndex(string(out), prompt); i >= 0 {
			out = out[:i]
			return out, nil
		}
	}
}

func StringFlags(opts ...string) func(t *testing.T, env Env) []string {
	return func(t *testing.T, env Env) []string { return opts }
}

var usedPorts = make(map[int]struct{})
var usedPortsMu sync.Mutex

func GetPort(t *testing.T) int {
	usedPortsMu.Lock()
	defer usedPortsMu.Unlock()
	for i := 8001; i < 9000; i++ {
		if _, ok := usedPorts[i]; !ok {
			usedPorts[i] = struct{}{}
			return i
		}
	}
	t.Fatalf("ports exhausted")
	return -1
}

func DonePort(i int) {
	usedPortsMu.Lock()
	defer usedPortsMu.Unlock()
	delete(usedPorts, i)
}

func ReadInt(t *testing.T, p string) int {
	d, err := os.ReadFile(p)
	assert.NilError(t, err)
	i, err := strconv.Atoi(string(d))
	assert.NilError(t, err)
	return i
}

func ReadString(t *testing.T, p string) string {
	d, err := os.ReadFile(p)
	assert.NilError(t, err)
	return string(d)
}

func StartHelloServer(t *testing.T) (pid int, port int) {
	port = GetPort(t)
	t.Logf("launching server on %d", port)
	cmd := exec.Command("httphello", fmt.Sprintf("localhost:%d", port))
	assert.NilError(t, cmd.Start())
	go func() {
		if err := cmd.Wait(); err != nil {
			t.Logf("hello server error: %v\n", err)
		}
		DonePort(port)
	}()
	for {
		if cmd.Process != nil {
			if _, err := http.Get(fmt.Sprintf("http://localhost:%d/", port)); err == nil {
				break
			}
		}
		time.Sleep(1 * time.Millisecond)
	}
	return cmd.Process.Pid, port
}

func StartDirServer(t *testing.T, dir string) (pid int, port int) {
	port = GetPort(t)
	t.Logf("launching server on %d", port)
	cmd := exec.Command("httphello", fmt.Sprintf("localhost:%d", port), dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	assert.NilError(t, cmd.Start())
	go func() {
		if err := cmd.Wait(); err != nil {
			t.Logf("dir server error: %v\n", err)
		}
		DonePort(port)
	}()
	for {
		if cmd.Process != nil {
			if _, err := http.Get(fmt.Sprintf("http://localhost:%d/", port)); err == nil {
				break
			}
		}
		time.Sleep(1 * time.Millisecond)
	}
	return cmd.Process.Pid, port
}
