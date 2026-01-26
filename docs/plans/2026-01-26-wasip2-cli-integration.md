# WASI P2 CLI Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable wasip2 containers to use `wasi:cli` interfaces for args/env, with a new config file for features not covered by the standard interfaces.

**Architecture:** Add wasip2 detection to init, use `os.Args` and `os.Environ` (which map to wasi:cli interfaces) for basic configuration. Create a minimal `/pack/wasi2-config` file for advanced features (networking, mounts). Embed default config in fs-wrapper for standalone operation.

**Tech Stack:** Go (init), Rust (fs-wrapper), WASI Preview 2

---

## Background

### Current `/mnt/wasi1/info` Directives

| Directive | Purpose | WASI P2 Equivalent |
|-----------|---------|-------------------|
| `c:` | Command args | ✅ `os.Args` → `wasi:cli/environment.get-arguments()` |
| `env:` | Environment vars | ✅ `os.Environ` → `wasi:cli/environment.get-environment()` |
| `e:` | Entrypoint | ✅ Can use args convention |
| `t:` | Set timestamp | ✅ `time.Now()` → `wasi:clocks/wall-clock.now()` (auto-sync) |
| `m:` | Bind mounts | ❌ None - requires host filesystem |
| `n:` | Networking/MAC | ❌ None - host-specific |
| `b:` | External bundle | ❌ None - host-specific |

### New Design

For wasip2 mode:
1. **Args/env** come from `wasi:cli` interfaces (native wasmtime support)
2. **Timestamp** auto-syncs from host via `wasi:clocks/wall-clock` (no config needed)
3. **Advanced config** read from `/pack/wasi2-config` for remaining features (mounts, networking, bundle)
4. **Detection** via `WASI_TARGET=p2` environment variable

---

### Task 1: Add wasip2 detection and mode constant

**Files:**
- Modify: `cmd/init/main.go:23-31` (constants section)

**Step 1: Add wasi2 constants**

Add after line 30 (`packFSTag = "wasi1"`):

```go
	// wasi2ConfigPath is the config file path for wasip2 mode
	wasi2ConfigPath = "/pack/wasi2-config"
)

// isWasiP2Mode returns true if running in WASI Preview 2 mode
func isWasiP2Mode() bool {
	return os.Getenv("WASI_TARGET") == "p2"
}
```

**Step 2: Verify syntax**

Run: `cd /home/jmlx/Projects/github.com/lx-industries/container2wasm && go build ./cmd/init`

Expected: Build succeeds

**Step 3: Commit**

```bash
git add cmd/init/main.go
git commit -m "feat(init): add wasip2 mode detection"
```

---

### Task 2: Create parseWasi2Config function

**Files:**
- Modify: `cmd/init/main.go` (add after `parseInfo` function, around line 481)

**Step 1: Add wasi2 config parser**

Add after the `parseInfo` function:

```go
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
```

**Step 2: Verify syntax**

Run: `go build ./cmd/init`

Expected: Build succeeds

**Step 3: Commit**

```bash
git add cmd/init/main.go
git commit -m "feat(init): add wasi2 config parser for advanced features"
```

---

### Task 3: Create getRuntimeFlagsFromCLI function

**Files:**
- Modify: `cmd/init/main.go` (add after `parseWasi2Config`)

**Step 1: Add CLI-based runtime flags function**

```go
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
```

**Step 2: Verify syntax**

Run: `go build ./cmd/init`

Expected: Build succeeds

**Step 3: Commit**

```bash
git add cmd/init/main.go
git commit -m "feat(init): add CLI-based runtime flags for wasip2"
```

---

### Task 4: Add clock auto-sync via wasi:clocks

**Files:**
- Modify: `cmd/init/main.go` (add after `getRuntimeFlagsFromCLI`)

**Step 1: Add syncVMClock function**

In wasip2 mode, the VM's internal clock should auto-sync to the host's wall clock.
Go's `time.Now()` maps to `wasi:clocks/wall-clock.now()` in WASI.

```go
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
```

**Step 2: Add time import if needed**

Ensure the `time` package is imported at the top of the file:

```go
import (
	// ... existing imports ...
	"time"
)
```

**Step 3: Verify syntax**

Run: `go build ./cmd/init`

Expected: Build succeeds

**Step 4: Commit**

```bash
git add cmd/init/main.go
git commit -m "feat(init): add VM clock sync via wasi:clocks"
```

---

### Task 5: Add wasip2 mode branch in doInit

**Files:**
- Modify: `cmd/init/main.go:120-151` (the NO_RUNTIME_CONFIG block for non-QEMU mode)

**Step 1: Read the current block**

The current code at lines 120-151 handles the legacy info file. We need to add a wasip2 branch.

**Step 2: Modify the block to handle wasip2**

Replace lines 120-151 with:

```go
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
```

**Step 3: Verify syntax**

Run: `go build ./cmd/init`

Expected: Build succeeds

**Step 4: Commit**

```bash
git add cmd/init/main.go
git commit -m "feat(init): add wasip2 mode branch using wasi:cli interfaces"
```

---

### Task 6: Create default wasi2-config for fs-wrapper

**Files:**
- Create: `extras/fs-wrapper/default-wasi2-config`

**Step 1: Create minimal default config**

The default config enables basic standalone operation with no special features:

```bash
cat > /home/jmlx/Projects/github.com/lx-industries/container2wasm/extras/fs-wrapper/default-wasi2-config << 'EOF'
# WASI P2 config - advanced features not covered by standard WASI interfaces
# Format: directive: value
#
# Available directives:
#   m: <path>     - Bind mount from /mnt/wasi0/<path> to /<path>
#   n: <mac>      - Enable networking with optional MAC address
#   b: <addr>     - External bundle address (9p=host:port)
#
# Note: These features are handled automatically by WASI standard interfaces:
#   - args, entrypoint, env: via wasi:cli (pass with: wasmtime run out.wasm -- /bin/sh)
#   - timestamp: auto-synced from host via wasi:clocks/wall-clock
#
# Default: no mounts, no networking (standalone container)
EOF
```

**Step 2: Verify file exists**

Run: `cat /home/jmlx/Projects/github.com/lx-industries/container2wasm/extras/fs-wrapper/default-wasi2-config`

Expected: Shows the config file contents

**Step 3: Commit**

```bash
git add extras/fs-wrapper/default-wasi2-config
git commit -m "feat(fs-wrapper): add default wasi2-config file"
```

---

### Task 7: Update fs-wrapper to embed wasi2-config

**Files:**
- Modify: `Dockerfile` (the fs-wrapper-with-files stage, around line 1110)

**Step 1: Find the fs-wrapper-with-files stage**

Search for: `FROM fs-wrapper-build AS fs-wrapper-with-files`

**Step 2: Add wasi2-config to the embedded files**

After copying rootfs.bin and boot.iso, add:

```dockerfile
# Copy wasi2-config for wasip2 standalone operation
COPY --link extras/fs-wrapper/default-wasi2-config /minpack/wasi2-config
```

**Step 3: Verify Dockerfile syntax**

Run: `docker build --check -f Dockerfile . 2>&1 | head -5 || echo "Syntax OK"`

**Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): embed wasi2-config in fs-wrapper"
```

---

### Task 8: Set WASI_TARGET env var in wasip2 build

**Files:**
- Modify: `Dockerfile` (bochs-dev-p2-wizer or bochs-dev-p2-packed stage)

**Step 1: Find where environment is set for the VM**

The init process needs to see `WASI_TARGET=p2`. This should be set in the init config that's baked into the image.

**Step 2: Find initconfig.json generation**

Search for `initconfig` in the Dockerfile or related files to understand how the init config is set.

Run: `grep -r "initconfig\|DebugInit\|INIT_DEBUG" Dockerfile | head -10`

**Step 3: Add WASI_TARGET to the VM environment**

The cleanest approach is to pass it as an environment variable to the wizer pre-initialization or set it in the Bochs build.

Add to the bochs-dev-p2-wizer stage, before the wizer command:

```dockerfile
ENV WASI_TARGET=p2
```

And ensure it's passed through to the init. This may require modifying how the VM is configured.

**Note:** This step may need investigation - document findings and adjust approach.

**Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): set WASI_TARGET=p2 for wasip2 builds"
```

---

### Task 9: Test wasip2 standalone operation

**Files:**
- None (testing only)

**Step 1: Build c2w**

```bash
go build -o c2w ./cmd/c2w
```

**Step 2: Build wasip2 image**

```bash
./c2w --target=wasi-p2 --assets . alpine:latest test-p2.wasm
```

**Step 3: Run with wasmtime - default shell**

```bash
timeout 30 wasmtime run test-p2.wasm 2>&1 | head -20
```

Expected: Container starts, enters default shell (from image config)

**Step 4: Run with wasmtime - custom command**

```bash
timeout 10 wasmtime run test-p2.wasm -- /bin/echo "hello from wasi p2" 2>&1
```

Expected: Prints "hello from wasi p2" and exits

**Step 5: Run with wasmtime - custom env**

```bash
timeout 10 wasmtime run --env MY_VAR=test123 test-p2.wasm -- /bin/sh -c 'echo $MY_VAR' 2>&1
```

Expected: Prints "test123"

**Step 6: Document results**

Note any issues for follow-up.

---

### Task 10: Update documentation

**Files:**
- Modify: `README.md` (wasip2 section)
- Modify: `extras/fs-wrapper/README.md`

**Step 1: Update main README**

Update the WASI Preview 2 section to show the new usage:

```markdown
### WASI Preview 2 Support (Experimental)

container2wasm can build images targeting WASI Preview 2 (Component Model):

\`\`\`console
$ c2w --target=wasi-p2 alpine:latest out.wasm
$ wasmtime run out.wasm
\`\`\`

The wasip2 output is a self-contained single file with embedded filesystem.

**Passing arguments and environment:**

\`\`\`console
# Custom command
$ wasmtime run out.wasm -- /bin/echo "hello"

# Custom environment
$ wasmtime run --env MY_VAR=value out.wasm -- /bin/sh -c 'echo $MY_VAR'
\`\`\`

> NOTE: wasi-p2 support is experimental with the following limitations:
> - Networking requires host-side configuration
> - Only Bochs (x86_64) emulator is currently supported
```

**Step 2: Update fs-wrapper README**

Add section about wasi2-config:

```markdown
## wasi2-config

For wasip2 builds, a `wasi2-config` file can be embedded to configure features
not covered by standard WASI interfaces:

- `m: <path>` - Bind mount from host
- `n: <mac>` - Enable networking
- `b: <addr>` - External bundle

Features handled automatically by WASI standard interfaces:
- Args/env: `wasi:cli` - pass via `wasmtime run out.wasm -- args`
- Timestamp: `wasi:clocks` - VM clock auto-syncs to host at boot
```

**Step 3: Commit**

```bash
git add README.md extras/fs-wrapper/README.md
git commit -m "docs: update wasip2 documentation for wasi:cli integration"
```

---

### Task 11: Push and update PR

**Files:**
- None (git operations only)

**Step 1: Push changes**

```bash
git push origin feature/wasip2-support
```

**Step 2: Add PR comment**

```bash
gh pr comment 565 --repo ktock/container2wasm --body "$(cat <<'EOF'
## WASI P2 CLI Integration

Added support for `wasi:cli` interfaces in wasip2 mode:

### Changes
- Init now detects wasip2 mode via `WASI_TARGET=p2` env var
- Args and env come from `wasi:cli/environment` interfaces
- New `/pack/wasi2-config` for advanced features (mounts, networking)
- Standalone operation works out of the box

### Usage
```bash
# Build
c2w --target=wasi-p2 alpine:latest out.wasm

# Run with defaults
wasmtime run out.wasm

# Run with custom command
wasmtime run out.wasm -- /bin/echo "hello"

# Run with env vars
wasmtime run --env FOO=bar out.wasm -- /bin/sh -c 'echo $FOO'
```
EOF
)"
```

---

## Verification Checklist

After completing all tasks:

1. **Build test:**
   ```bash
   ./c2w --target=wasi-p2 alpine:latest test.wasm
   ```

2. **Default run:**
   ```bash
   wasmtime run test.wasm
   # Should start default shell
   ```

3. **Custom command:**
   ```bash
   wasmtime run test.wasm -- /bin/echo "test"
   # Should print "test"
   ```

4. **Environment passing:**
   ```bash
   wasmtime run --env TEST=123 test.wasm -- /bin/sh -c 'echo $TEST'
   # Should print "123"
   ```

5. **Regression (wasip1):**
   ```bash
   ./c2w --target=wasi-p1 alpine:latest test-p1.wasm
   wasmtime run --dir .::/ test-p1.wasm
   # Should work as before
   ```
