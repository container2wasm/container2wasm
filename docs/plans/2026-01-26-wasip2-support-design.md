# WASI Preview 2 Support for Bochs (x86_64)

## Overview

Add opt-in WASI Preview 2 (wasip2) support for Bochs-based x86_64 containers, targeting server-side WASI runtimes.

**Related issue:** https://github.com/container2wasm/container2wasm/issues/362

> **Implementation Note:** The actual implementation uses a different approach than
> originally designed. Instead of wasi-virt, we use a custom fs-wrapper component
> composed via `wac plug`. This approach was necessary due to WASI version compatibility
> issues between the preview1 adapter and wasi-virt. See `extras/fs-wrapper/README.md`
> for details on the filesystem embedding approach.

## Goals

- New CLI flag: `--target=wasi-p2` produces Component Model wasm output
- Default remains wasip1 (no breaking changes)
- Target runtimes: wasmtime, wasmer, wasmedge, wazero
- Basic container execution (shell, file I/O)

## Non-Goals (Deferred)

- TinyEMU (RISC-V) support - apply same pattern later
- QEMU/browser support - future work
- wasip2 native networking - future phase

## Toolchain Changes

### Current (wasip1)

| Tool | Version | Purpose |
|------|---------|---------|
| wasi-sdk | v19 | Compile C/C++ to wasm32-wasi |
| wasi-vfs | v0.3.0 | Package filesystem into wasm |
| wizer | commit 04e49c9 | Pre-initialize VM state |
| binaryen | v114 | Asyncify optimization |

### Proposed (wasip2)

| Tool | Version | Purpose |
|------|---------|---------|
| wasi-sdk | v24+ | Compile C/C++ to wasm32-wasip2 |
| wasi-virt | latest | Virtualize WASI interfaces (replaces wasi-vfs) |
| wizer | latest | Pre-initialize VM state (has wasip2 support) |
| binaryen | v114 | Asyncify optimization (unchanged) |

### Build Pipeline Order (wasip2)

1. Compile Bochs with wasi-sdk targeting `wasm32-wasip2`
2. Apply asyncify (binaryen) - operates on core wasm before componentization
3. Pre-initialize with wizer
4. Virtualize filesystem with wasi-virt
5. Output: Component Model wasm

## Dockerfile Structure

Shared base stages (WASI-version agnostic):
- Linux kernel compilation
- Rootfs creation (busybox, runc, init, tini)
- GRUB bootloader
- Bochs source preparation

Separate paths for p1 and p2:

```dockerfile
# ===== SHARED STAGES (unchanged) =====
FROM ... AS linux-kernel-dev
FROM ... AS rootfs-dev
FROM ... AS grub-dev
FROM ... AS bochs-src

# ===== WASIP1 PATH =====
FROM ... AS toolchain-p1
# wasi-sdk v19, wasi-vfs, wizer (current versions)

FROM ... AS bochs-p1
# Compile and package for wasip1

# ===== WASIP2 PATH =====
FROM ... AS toolchain-p2
# wasi-sdk v24, wasi-virt, wizer (latest)

FROM ... AS bochs-p2
# Compile and package for wasip2

# ===== OUTPUT =====
ARG WASI_TARGET=p1
FROM bochs-${WASI_TARGET} AS bochs-dev-packed
```

## CLI Integration

New flag for `c2w` command:

```
c2w --target=wasi-p2 alpine:latest out.wasm
```

- `--target=wasi-p1` (default): Current wasip1 output
- `--target=wasi-p2`: Component Model wasip2 output

Passed to Docker build as `--build-arg WASI_TARGET=p2`.

## Risks & Mitigations

### Bochs source compatibility with wasip2

WASI p1 and p2 have API differences. Bochs uses a custom fork with WASI patches.

**Mitigation:** wasi-sdk's p2 libc should handle most differences transparently. May need minor source patches if not.

### Asyncify + Component Model interaction

Asyncify transforms must happen on core wasm before componentization.

**Mitigation:** Build order is critical - verify pipeline produces valid output early.

### Wizer wasip2 support maturity

Wizer's wasip2 support is relatively new.

**Mitigation:** Test early with Bochs-sized modules, report issues upstream if found.

## Implementation Plan

### Phase 1: Toolchain Validation

1. Verify wasi-sdk v24+ compiles simple C program to wasip2
2. Test wasi-virt CLI filesystem virtualization
3. Test wizer with wasip2 target
4. Document exact working versions

### Phase 2: Dockerfile Changes

1. Add `WASI_TARGET` build arg
2. Create `toolchain-p2` stage
3. Create `bochs-p2` compilation stage
4. Create `bochs-p2-packed` packaging stage
5. Wire up stage selection

### Phase 3: CLI Integration

1. Add `--target` flag to `c2w` command
2. Pass through as Docker build arg
3. Update help text and validation

### Phase 4: Testing

1. Build: `c2w --target=wasi-p2 alpine:latest out.wasm`
2. Run: `wasmtime run out.wasm`
3. Verify basic operations
4. Regression test wasip1 path

### Phase 5: Documentation

1. Update README with wasip2 option
2. Document runtime requirements

## References

- [WASI Preview 2 spec](https://github.com/WebAssembly/wasi)
- [wasi-virt](https://github.com/bytecodealliance/wasi-virt) - wasip2 filesystem virtualization
- [wasi-sdk releases](https://github.com/WebAssembly/wasi-sdk/releases)
- [Original issue discussion](https://github.com/container2wasm/container2wasm/issues/362)
