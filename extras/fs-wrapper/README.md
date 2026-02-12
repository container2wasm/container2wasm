# fs-wrapper

A WASI Component that provides `wasi:filesystem` interfaces with embedded files.

## Purpose

This component is used in the container2wasm wasip2 build pipeline to embed
runtime files (like `rootfs.bin` and `boot.iso`) directly into the WASM binary.
It exports `wasi:filesystem/types` and `wasi:filesystem/preopens`, allowing it
to be composed with emulator components (like Bochs) using `wac plug`.

## How it works

1. **Build time**: The `build.rs` script scans a directory for files
2. **Compilation**: Files are embedded via `include_bytes!` into the WASM
3. **Runtime**: The component exposes embedded files at `/pack` via WASI filesystem interfaces

## Building

### Environment variable

Set `FS_WRAPPER_PACK_DIR` to the directory containing files to embed:

```bash
FS_WRAPPER_PACK_DIR=/path/to/files cargo component build --release
```

If not set, defaults to `./pack` (which is gitignored).

### Docker build integration

In the Dockerfile, fs-wrapper is built after wizer pre-initialization:

```dockerfile
FROM fs-wrapper-build AS fs-wrapper-with-files
COPY --from=bochs-dev-p2-wizer /minpack /minpack
ENV FS_WRAPPER_PACK_DIR=/minpack
RUN cargo component build --release
```

The `/minpack` directory is created by the wizer stage and contains only the
files needed at runtime (typically `rootfs.bin` and `boot.iso`). Other files
like BIOS ROMs and config files are read during wizer pre-initialization and
their state is baked into the emulator WASM.

## Composition

The built component is composed with the emulator using `wac plug`:

```bash
wac plug bochs.component.wasm --plug fs_wrapper.wasm -o bochs.composed.wasm
```

This satisfies the emulator's `wasi:filesystem` imports with the fs-wrapper's
exports, creating a self-contained WASM component with embedded filesystem.

## Files exposed

All files in `FS_WRAPPER_PACK_DIR` are embedded and exposed under `/pack`:

- `/pack/rootfs.bin` - Container filesystem image
- `/pack/boot.iso` - Bootable ISO (for Bochs)
- (any other files in the source directory)

## Limitations

- Read-only filesystem (all write operations return `ErrorCode::ReadOnly`)
- Single directory level (no subdirectories)
- No streaming I/O (use `read()` method instead of streams)

## Why WIT files are bundled in the repository

The `wit/deps/` directory contains WASI WIT files sourced directly from wasmtime
v41.0.0 rather than using cargo-component's registry. This is intentional.

### WASI Version Compatibility

There's a version mismatch in the current WASI ecosystem:

| Component | WASI Version |
|-----------|--------------|
| cargo-component registry | 0.2.9 only |
| Wasmtime v41.0.0 preview1 adapter | 0.2.6 |

When composing components with `wac plug`, strict version matching is enforced.
A component exporting `wasi:filesystem@0.2.9` cannot plug into imports expecting
`wasi:filesystem@0.2.6`, even though they are semantically compatible.

### Preserving WASI P1 Compatibility

The preview1 adapter is essential for running existing WASI P1 code (like Bochs)
as WASI P2 components. It converts legacy `fd_read`, `fd_write`, etc. calls to
the component model's `wasi:filesystem` interfaces.

Since the adapter produces components with 0.2.6 imports, we must export 0.2.6
interfaces. By bundling the WIT files from wasmtime's source, we ensure exact
version alignment and successful component composition.

### Removing @unstable annotations

The bundled WIT files have `@unstable` annotations removed. cargo-component by
default excludes interfaces marked unstable, but some stable interfaces import
from unstable ones (e.g., `wasi:cli` imports `wasi:clocks/timezone`), causing
build failures. Removing the annotations makes all interfaces available.

### When this can change

This workaround will no longer be necessary when either:
- The wasmtime preview1 adapter is updated to use WASI 0.2.9+
- Composition tools (wac, wasm-compose) support version aliasing
- cargo-component adds a flag to enable unstable features
