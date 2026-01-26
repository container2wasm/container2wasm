# WASI Preview 2 Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add opt-in wasip2 support for Bochs (x86_64) containers with `--target=wasi-p2` flag.

**Architecture:** Parallel Dockerfile stages for p1/p2 toolchains, CLI flag passes build arg, wasi-virt replaces wasi-vfs for Component Model packaging.

**Tech Stack:** wasi-sdk v27, wasi-virt (git main), wizer v8.0.0, binaryen v114

---

## Task 1: Add WASI_TARGET Build Arg to Dockerfile

**Files:**
- Modify: `Dockerfile:1-30` (ARG declarations)

**Step 1: Add the WASI_TARGET argument**

After line 29 (`ARG OPTIMIZATION_MODE=wizer`), add:

```dockerfile
ARG WASI_TARGET=p1 # p1 (default, wasip1) or p2 (wasip2)
```

**Step 2: Add wasip2 toolchain version arguments**

After the existing version ARGs (around line 10), add:

```dockerfile
ARG WASI_SDK_VERSION_P2=27
ARG WASI_SDK_VERSION_P2_FULL=${WASI_SDK_VERSION_P2}.0
ARG WIZER_VERSION_P2=v8.0.0
```

**Step 3: Verify syntax**

Run: `docker build --check -f Dockerfile .` (or just ensure file parses)

**Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add WASI_TARGET build arg for wasip2 support"
```

---

## Task 2: Create wasip2 Toolchain Stage

**Files:**
- Modify: `Dockerfile` (add new stage after line 997, before bochs-repo COPY)

**Step 1: Add the toolchain-p2 stage**

Insert after `bochs-dev-common` stage (around line 1023), a new stage:

```dockerfile
# ===== WASIP2 TOOLCHAIN =====
FROM rust:1.74.1-bullseye AS bochs-toolchain-p2
ARG WASI_SDK_VERSION_P2
ARG WASI_SDK_VERSION_P2_FULL
ARG WIZER_VERSION_P2
ARG BINARYEN_VERSION
RUN apt-get update -y && apt-get install -y make curl git gcc xz-utils

# wasi-sdk v27 with wasip2 support
WORKDIR /wasi
RUN curl -o wasi-sdk.tar.gz -fSL https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION_P2}/wasi-sdk-${WASI_SDK_VERSION_P2_FULL}-x86_64-linux.tar.gz && \
    tar xvf wasi-sdk.tar.gz && rm wasi-sdk.tar.gz
ENV WASI_SDK_PATH=/wasi/wasi-sdk-${WASI_SDK_VERSION_P2_FULL}

# wasi-virt for Component Model filesystem virtualization
WORKDIR /work/
RUN git clone https://github.com/bytecodealliance/wasi-virt.git && \
    cd wasi-virt && \
    cargo build --release && \
    mkdir -p /tools/wasi-virt/ && \
    mv target/release/wasi-virt /tools/wasi-virt/ && \
    cargo clean

# wizer with wasip2 support
WORKDIR /work/
RUN git clone https://github.com/bytecodealliance/wizer && \
    cd wizer && \
    git checkout "${WIZER_VERSION_P2}" && \
    cargo build --bin wizer --all-features --release && \
    mkdir -p /tools/wizer/ && \
    mv include target/release/wizer /tools/wizer/ && \
    cargo clean

# binaryen (same version as p1)
RUN wget -O /tmp/binaryen.tar.gz https://github.com/WebAssembly/binaryen/releases/download/version_${BINARYEN_VERSION}/binaryen-version_${BINARYEN_VERSION}-x86_64-linux.tar.gz
RUN mkdir -p /binaryen
RUN tar -C /binaryen -zxvf /tmp/binaryen.tar.gz
```

**Step 2: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add wasip2 toolchain stage with wasi-sdk v27 and wasi-virt"
```

---

## Task 3: Create Bochs wasip2 Compilation Stage

**Files:**
- Modify: `Dockerfile` (add after toolchain-p2 stage)

**Step 1: Add bochs-dev-p2 compilation stage**

This stage compiles Bochs for wasip2. Insert after toolchain-p2:

```dockerfile
# ===== BOCHS WASIP2 COMPILATION =====
FROM bochs-toolchain-p2 AS bochs-dev-p2-common
COPY --link --from=bochs-repo / /Bochs

# Build JMP module for wasip2
WORKDIR /Bochs/bochs/wasi_extra/jmp
RUN mkdir /jmp && cp jmp.h /jmp/
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-wasip2 -c jmp.c -I . -o jmp.o
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-wasip2 -Wl,--export=wasm_setjmp -c jmp.S -o jmp_wrapper.o
RUN ${WASI_SDK_PATH}/bin/wasm-ld jmp.o jmp_wrapper.o --export=wasm_setjmp --export=wasm_longjmp --export=handle_jmp --no-entry -r -o /jmp/jmp

# Build VFS module for wasip2
WORKDIR /Bochs/bochs/wasi_extra/vfs
RUN mkdir /vfs
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-wasip2 -c vfs.c -I . -o /vfs/vfs.o

# Configure and build Bochs for wasip2
WORKDIR /Bochs/bochs
ARG INIT_DEBUG
RUN LOGGING_FLAG=--disable-logging && \
    if test "${INIT_DEBUG}" = "true" ; then LOGGING_FLAG=--enable-logging ; fi && \
    CC="${WASI_SDK_PATH}/bin/clang" CXX="${WASI_SDK_PATH}/bin/clang++" RANLIB="${WASI_SDK_PATH}/bin/ranlib" \
    CFLAGS="--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot --target=wasm32-wasip2 -D_WASI_EMULATED_SIGNAL -DWASI -D__GNU__ -O2 -I/jmp/ -I/tools/wizer/include/" \
    CXXFLAGS="${CFLAGS}" \
    ./configure --host wasm32-wasip2 --enable-x86-64 --with-nogui --enable-usb --enable-usb-ehci \
    --disable-large-ramfile --disable-show-ips --disable-stats ${LOGGING_FLAG} \
    --enable-repeat-speedups --enable-fast-function-calls --disable-trace-linking --enable-handlers-chaining --enable-avx
RUN make -j$(nproc) bochs EMU_DEPS="/jmp/jmp /vfs/vfs.o -lrt"

# Apply asyncify (must be done before componentization)
RUN /binaryen/binaryen-version_${BINARYEN_VERSION}/bin/wasm-opt bochs --asyncify -O2 -o bochs.async --pass-arg=asyncify-ignore-imports
RUN mv bochs.async bochs
```

**Step 2: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add Bochs wasip2 compilation stage"
```

---

## Task 4: Create Bochs wasip2 Packaging Stages

**Files:**
- Modify: `Dockerfile` (add wizer and packaging stages for p2)

**Step 1: Add wizer and packaging stages for wasip2**

```dockerfile
FROM bochs-dev-p2-common AS bochs-dev-p2-native
COPY --link --from=vm-amd64-dev /pack /minpack

FROM bochs-dev-p2-common AS bochs-dev-p2-wizer
COPY --link --from=vm-amd64-dev /pack /pack
ENV WASMTIME_BACKTRACE_DETAILS=1
# Note: wizer for wasip2 may need different flags - test this
RUN mv bochs bochs-org && /tools/wizer/wizer --allow-wasi --wasm-bulk-memory=true -r _start=wizer.resume --mapdir /pack::/pack -o bochs bochs-org
RUN mkdir /minpack && cp /pack/rootfs.bin /minpack/ && cp /pack/boot.iso /minpack/

FROM bochs-dev-p2-${OPTIMIZATION_MODE} AS bochs-dev-p2-packed
# Use wasi-virt instead of wasi-vfs for Component Model
RUN /tools/wasi-virt/wasi-virt bochs --mapdir /pack::/minpack -o packed && mkdir /out
ARG OUTPUT_NAME
RUN mv packed /out/$OUTPUT_NAME
```

**Step 2: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add Bochs wasip2 wizer and packaging stages"
```

---

## Task 5: Wire Up WASI_TARGET Stage Selection

**Files:**
- Modify: `Dockerfile` (modify final stage selection around line 1039)

**Step 1: Create conditional stage selection**

Replace the existing final stage selection with conditional logic. Modify around line 1039:

```dockerfile
# ===== WASI TARGET SELECTION =====
FROM bochs-dev-packed AS bochs-final-p1
FROM bochs-dev-p2-packed AS bochs-final-p2
FROM bochs-final-${WASI_TARGET} AS bochs-final

FROM scratch AS wasi-amd64
COPY --link --from=bochs-final /out/ /
```

**Step 2: Test the build with default (p1)**

Run: `docker build --build-arg TARGETARCH=amd64 --target=wasi-amd64 -f Dockerfile . 2>&1 | head -50`

Expected: Build starts successfully with existing p1 stages

**Step 3: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): wire up WASI_TARGET stage selection for p1/p2"
```

---

## Task 6: Add --target Flag to CLI

**Files:**
- Modify: `cmd/c2w/main.go:31-86` (flag definitions)
- Modify: `cmd/c2w/main.go:184-253` (build function)
- Modify: `cmd/c2w/main.go:255-317` (buildWithLegacyBuilder function)

**Step 1: Add the target flag definition**

In the flags slice (around line 31), add after the `target-arch` flag:

```go
cli.StringFlag{
    Name:  "target",
    Usage: "WASI target: wasi-p1 (default) or wasi-p2",
    Value: "wasi-p1",
},
```

**Step 2: Add validation for target flag**

In `rootAction` function, after line 130 (after legacy check), add:

```go
target := clicontext.String("target")
if target != "wasi-p1" && target != "wasi-p2" {
    return fmt.Errorf("invalid target %q: must be wasi-p1 or wasi-p2", target)
}
```

**Step 3: Pass target to build function**

In the `build` function (around line 238), add after the INIT_DEBUG build arg:

```go
// WASI target (p1 or p2)
wasiTarget := "p1"
if clicontext.String("target") == "wasi-p2" {
    wasiTarget = "p2"
}
buildxArgs = append(buildxArgs, "--build-arg", fmt.Sprintf("WASI_TARGET=%s", wasiTarget))
```

**Step 4: Pass target to legacy build function**

In `buildWithLegacyBuilder` function (around line 302), add similar logic:

```go
// WASI target (p1 or p2)
wasiTarget := "p1"
if clicontext.String("target") == "wasi-p2" {
    wasiTarget = "p2"
}
buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("WASI_TARGET=%s", wasiTarget))
```

**Step 5: Verify compilation**

Run: `go build ./cmd/c2w`

Expected: Compiles without errors

**Step 6: Commit**

```bash
git add cmd/c2w/main.go
git commit -m "feat(cli): add --target flag for wasi-p1/wasi-p2 selection"
```

---

## Task 7: Test wasip1 Regression

**Files:**
- None (testing only)

**Step 1: Build a test image with wasip1 (default)**

Run: `./c2w alpine:latest test-p1.wasm`

Expected: Build completes successfully, produces test-p1.wasm

**Step 2: Run with wasmtime**

Run: `wasmtime run test-p1.wasm`

Expected: Container boots (may need `--mapdir` flags for full operation)

**Step 3: Document results**

Note any issues or confirm success.

---

## Task 8: Test wasip2 Build

**Files:**
- None (testing only)

**Step 1: Build a test image with wasip2**

Run: `./c2w --target=wasi-p2 alpine:latest test-p2.wasm`

Expected: Build completes (may have issues to debug)

**Step 2: Inspect output**

Run: `file test-p2.wasm` and `wasm-tools component wit test-p2.wasm`

Expected: Should show Component Model format

**Step 3: Run with wasmtime**

Run: `wasmtime run test-p2.wasm`

Expected: Container boots

**Step 4: Document results and fix issues**

This step may require multiple iterations to resolve toolchain issues.

---

## Task 9: Update README Documentation

**Files:**
- Modify: `README.md`

**Step 1: Add wasip2 documentation**

Add a section about the new `--target` flag:

```markdown
### WASI Preview 2 Support (Experimental)

To build a container image targeting WASI Preview 2 (Component Model):

\`\`\`bash
c2w --target=wasi-p2 alpine:latest out.wasm
\`\`\`

This produces a Component Model wasm file that can be run on wasip2-compatible runtimes:

\`\`\`bash
wasmtime run out.wasm
\`\`\`

**Note:** wasip2 support is experimental. Networking is not yet implemented for wasip2.
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add wasip2 --target flag documentation"
```

---

## Task 10: Final Integration Test and PR

**Files:**
- None

**Step 1: Run full test suite**

If tests exist: `make test` or equivalent

**Step 2: Create PR**

```bash
git push origin feature/wasip2-support
gh pr create --title "feat: add WASI Preview 2 support for Bochs (x86_64)" --body "$(cat <<'EOF'
## Summary
- Adds `--target=wasi-p2` flag to produce Component Model wasm output
- Uses wasi-sdk v27, wasi-virt, and wizer v8.0.0 for wasip2 toolchain
- Default remains wasip1 (no breaking changes)

## Test plan
- [ ] Build with `--target=wasi-p1` (regression test)
- [ ] Build with `--target=wasi-p2`
- [ ] Run wasip2 output on wasmtime

Closes #362
EOF
)"
```

---

## Known Risks and Debugging Tips

### If Bochs fails to compile for wasip2:
- Check that `--target=wasm32-wasip2` is supported by the wasi-sdk version
- The JMP and VFS modules may need adjustments for wasip2 ABI

### If wizer fails on wasip2:
- wizer v8.0.0 should support wasip2, but may need `--wasi` flags adjusted
- Try running wizer with `RUST_BACKTRACE=1` for debugging

### If wasi-virt fails:
- Check that wasi-virt's `--mapdir` syntax matches expectations
- May need to use `wasm-tools compose` instead for complex cases

### Component Model verification:
- Use `wasm-tools component wit <file>` to inspect the component
- Use `wasm-tools validate --features component-model <file>` to validate
