package main

import (
	"os"
	"context"
	crand "crypto/rand"
	"bytes"
	"io"
	"flag"
	"strings"
	
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

func main(){
	var (
		mapDir   = flag.String("mapdir", "", "directory mapping to the image")
	)

	flag.Parse()
	args := flag.Args()
	fsConfig := wazero.NewFSConfig()
	if mapDir != nil && *mapDir != "" {
		m := strings.SplitN(*mapDir, "::", 2)
		if len(m) != 2 {
			panic("specify mapdir as dst::src")
		}
		fsConfig = fsConfig.WithDirMount(m[1], m[0])
	}
	
	ctx := context.TODO()
	c, err := os.ReadFile(args[0])
	if err != nil {
		panic(err)
	}
	r := wazero.NewRuntime(ctx)
	defer func() {
		r.Close(ctx)
	}()
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	if _, err := r.NewHostModuleBuilder("env").Instantiate(ctx); err != nil {
		panic(err)
	}
	compiled, err := r.CompileModule(ctx, c)
	if err != nil {
		panic(err)
	}
	// we forciblly enable non-blocking read of stdin.
	_, err = r.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithSysWalltime().WithSysNanotime().WithSysNanosleep().WithRandSource(crand.Reader).WithStdout(os.Stdout).WithStderr(os.Stderr).WithStdin(newNonBlockReader(os.Stdin)).WithFSConfig(fsConfig).WithArgs(append([]string{"arg0"}, args[1:]...)...))
	if err != nil {
		if exitErr, ok := err.(*sys.ExitError); ok && exitErr.ExitCode() == 0 {
			return
		}
		panic(err)
	}
	return
}

type nonBlockReader struct {
	buf *bytes.Buffer
	closed bool
}

func newNonBlockReader(r io.Reader) io.Reader {
	buf := new(bytes.Buffer) // TODO: FIXME: handle the situation where written bytes exceed the buffer size
	br := &nonBlockReader{buf: buf}
	go func(){
		defer func(){
			br.closed = true
		}()
		var p [1]byte
		for {
			n, err := r.Read(p[:])
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			if n > 0 {
				buf.Write(p[:])
			}
			
		}
	}()
	return br
}

func (r *nonBlockReader) Read(p []byte) (int, error) {
	n, err := r.buf.Read(p)
	if err == io.EOF && !r.closed {
		// TinyEMU requires n < -1 if the reader isn't closed yet
		n, err = -1, nil
	}
	return n, err
}
