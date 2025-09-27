package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ktock/container2wasm/tests/integration/utils"
)

func TestTools(t *testing.T) {
	utils.RunTestRuntimes(t, []utils.TestSpec{
		{
			Name:    "net-proxy",
			Runtime: "c2w-net-proxy-test",
			Inputs: []utils.Input{
				{Image: "debian-wget-x86-64", Architecture: utils.X8664, Dockerfile: `
FROM debian:sid-slim
RUN apt-get update && apt-get install -y wget
`},
				{Image: "debian-wget-rv64", ConvertOpts: []string{"--target-arch=riscv64"}, Architecture: utils.RISCV64, Dockerfile: `
FROM riscv64/debian:sid-slim
RUN apt-get update && apt-get install -y wget
`, BuildArgs: []string{"--platform=linux/riscv64"}},
			},
			Prepare: func(t *testing.T, env utils.Env) {
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httphello-vm-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httphello-stack-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
				pid, port := utils.StartHelloServer(t)
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httphello-pid"), []byte(fmt.Sprintf("%d", pid)), 0755))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httphello-port"), []byte(fmt.Sprintf("%d", port)), 0755))
			},
			Finalize: func(t *testing.T, env utils.Env) {
				p, err := os.FindProcess(utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-pid")))
				assert.NilError(t, err)
				assert.NilError(t, p.Kill())
				if _, err := p.Wait(); err != nil {
					t.Logf("hello server error: %v\n", err)
				}
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-vm-port")))
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-stack-port")))
			},
			RuntimeOpts: func(t *testing.T, env utils.Env) []string {
				return []string{
					"--stack=" + utils.C2wNetProxyBin,
					fmt.Sprintf("--stack-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-stack-port"))),
					fmt.Sprintf("--vm-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-vm-port"))),
				}
			},
			Args: func(t *testing.T, env utils.Env) []string {
				return []string{"--net=socket=listenfd=4", "sh", "-c", fmt.Sprintf("for I in $(seq 1 50) ; do if wget -O - http://127.0.0.1:%d/ 2>/dev/null ; then break ; fi ; sleep 1 ; done", utils.ReadInt(t, filepath.Join(env.Workdir, "httphello-port")))}
			},
			Want: utils.WantString("hello"),
		},
		{
			Name:    "imagemounter-registry",
			Runtime: "imagemounter-test",
			Inputs: []utils.Input{
				{Image: "alpine:3.17", Mirror: true, Architecture: utils.X8664, ConvertOpts: []string{"--external-bundle"}, External: true},
				{Image: "ghcr.io/stargz-containers/ubuntu:22.04-esgz", Mirror: true, Architecture: utils.X8664, ConvertOpts: []string{"--external-bundle"}, External: true},
				{Image: "riscv64/alpine:20221110", Mirror: true, Architecture: utils.RISCV64, ConvertOpts: []string{"--target-arch=riscv64", "--external-bundle"}, External: true},
			},
			Prepare: func(t *testing.T, env utils.Env) {
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "imagemountertest-vm-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "imagemountertest-stack-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
			},
			Finalize: func(t *testing.T, env utils.Env) {
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-vm-port")))
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-stack-port")))
			},
			RuntimeOpts: func(t *testing.T, env utils.Env) []string {
				return []string{
					"--image", "localhost:5000/" + env.Input.Image,
					"--stack", utils.ImageMounterBin,
					fmt.Sprintf("--stack-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-stack-port"))),
					fmt.Sprintf("--vm-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-vm-port"))),
				}
			},
			Args: func(t *testing.T, env utils.Env) []string {
				return []string{"--net=socket=listenfd=4", "--external-bundle=9p=192.168.127.252", "echo", "-n", "hello"}
			},
			Want: utils.WantString("hello"),
		},
		{
			Name:    "imagemounter-store",
			Runtime: "imagemounter-test",
			Inputs: []utils.Input{
				{Image: "alpine:3.17", Store: "testimage", Architecture: utils.X8664, ConvertOpts: []string{"--external-bundle"}, External: true},
				{Image: "ghcr.io/stargz-containers/ubuntu:22.04-esgz", Store: "testimage", Architecture: utils.X8664, ConvertOpts: []string{"--external-bundle"}, External: true},
				{Image: "riscv64/alpine:20221110", Store: "testimage", Architecture: utils.RISCV64, ConvertOpts: []string{"--target-arch=riscv64", "--external-bundle"}, External: true},
			},
			Prepare: func(t *testing.T, env utils.Env) {
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "imagemountertest-vm-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "imagemountertest-stack-port"), []byte(fmt.Sprintf("%d", utils.GetPort(t))), 0755))
				pid, port := utils.StartDirServer(t, filepath.Join(env.Workdir, env.Input.Store))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httpdir-pid"), []byte(fmt.Sprintf("%d", pid)), 0755))
				assert.NilError(t, os.WriteFile(filepath.Join(env.Workdir, "httpdir-port"), []byte(fmt.Sprintf("%d", port)), 0755))
			},
			Finalize: func(t *testing.T, env utils.Env) {
				p, err := os.FindProcess(utils.ReadInt(t, filepath.Join(env.Workdir, "httpdir-pid")))
				assert.NilError(t, err)
				assert.NilError(t, p.Kill())
				if _, err := p.Wait(); err != nil {
					t.Logf("dir server error: %v\n", err)
				}
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-vm-port")))
				utils.DonePort(utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-stack-port")))
			},
			RuntimeOpts: func(t *testing.T, env utils.Env) []string {
				return []string{
					"--image", fmt.Sprintf("http://localhost:%d/", utils.ReadInt(t, filepath.Join(env.Workdir, "httpdir-port"))),
					"--stack", utils.ImageMounterBin,
					fmt.Sprintf("--stack-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-stack-port"))),
					fmt.Sprintf("--vm-port=%d", utils.ReadInt(t, filepath.Join(env.Workdir, "imagemountertest-vm-port"))),
				}
			},
			Args: func(t *testing.T, env utils.Env) []string {
				return []string{"--net=socket=listenfd=4", "--external-bundle=9p=192.168.127.252", "echo", "-n", "hello"}
			},
			Want: utils.WantString("hello"),
		},
	}...)
}
