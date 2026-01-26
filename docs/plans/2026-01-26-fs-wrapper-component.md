# Filesystem Wrapper Component Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create a single-file wasip2 output with embedded filesystem by implementing a minimal wasi:filesystem wrapper component and composing it with Bochs using `wac plug`.

**Architecture:** A Rust component exports wasi:filesystem interfaces with files embedded via `include_bytes!`. At Docker build time, VM files are copied into the wrapper project, compiled into the component, then composed with Bochs using `wac plug` to satisfy filesystem imports.

**Tech Stack:** Rust, wit-bindgen, cargo-component, wac-cli, wasm-tools

---

### Task 1: Create fs-wrapper Rust project structure

**Files:**
- Create: `extras/fs-wrapper/Cargo.toml`
- Create: `extras/fs-wrapper/src/lib.rs` (empty placeholder)

**Step 1: Create directory structure**

```bash
mkdir -p extras/fs-wrapper/src
mkdir -p extras/fs-wrapper/wit/deps
```

**Step 2: Create Cargo.toml**

Create `extras/fs-wrapper/Cargo.toml`:

```toml
[package]
name = "fs-wrapper"
version = "0.1.0"
edition = "2021"

[lib]
crate-type = ["cdylib"]

[dependencies]
wit-bindgen = "0.41"

[package.metadata.component]
package = "c2w:fs-wrapper"

[package.metadata.component.target]
path = "wit"

[package.metadata.component.target.dependencies]
"wasi:io" = { path = "wit/deps/io" }
"wasi:clocks" = { path = "wit/deps/clocks" }
"wasi:filesystem" = { path = "wit/deps/filesystem" }
```

**Step 3: Create placeholder lib.rs**

Create `extras/fs-wrapper/src/lib.rs`:

```rust
// Filesystem wrapper component for container2wasm wasip2 support
// Implements minimal wasi:filesystem interfaces with embedded files
```

**Step 4: Verify directory structure**

Run: `find extras/fs-wrapper -type f`

Expected:
```
extras/fs-wrapper/Cargo.toml
extras/fs-wrapper/src/lib.rs
```

**Step 5: Commit**

```bash
git add extras/fs-wrapper/
git commit -m "feat(fs-wrapper): create Rust project structure"
```

---

### Task 2: Add WASI WIT dependencies

**Files:**
- Create: `extras/fs-wrapper/wit/world.wit`
- Create: `extras/fs-wrapper/wit/deps/io/world.wit`
- Create: `extras/fs-wrapper/wit/deps/clocks/world.wit`
- Create: `extras/fs-wrapper/wit/deps/filesystem/world.wit`

**Step 1: Download WASI wit files**

```bash
cd extras/fs-wrapper/wit/deps

# io package
mkdir -p io && curl -fSL -o io/world.wit https://raw.githubusercontent.com/WebAssembly/wasi-io/v0.2.3/wit/world.wit
curl -fSL -o io/error.wit https://raw.githubusercontent.com/WebAssembly/wasi-io/v0.2.3/wit/error.wit
curl -fSL -o io/poll.wit https://raw.githubusercontent.com/WebAssembly/wasi-io/v0.2.3/wit/poll.wit
curl -fSL -o io/streams.wit https://raw.githubusercontent.com/WebAssembly/wasi-io/v0.2.3/wit/streams.wit

# clocks package
mkdir -p clocks && curl -fSL -o clocks/world.wit https://raw.githubusercontent.com/WebAssembly/wasi-clocks/v0.2.3/wit/world.wit
curl -fSL -o clocks/monotonic-clock.wit https://raw.githubusercontent.com/WebAssembly/wasi-clocks/v0.2.3/wit/monotonic-clock.wit
curl -fSL -o clocks/wall-clock.wit https://raw.githubusercontent.com/WebAssembly/wasi-clocks/v0.2.3/wit/wall-clock.wit

# filesystem package
mkdir -p filesystem && curl -fSL -o filesystem/world.wit https://raw.githubusercontent.com/WebAssembly/wasi-filesystem/v0.2.3/wit/world.wit
curl -fSL -o filesystem/types.wit https://raw.githubusercontent.com/WebAssembly/wasi-filesystem/v0.2.3/wit/types.wit
curl -fSL -o filesystem/preopens.wit https://raw.githubusercontent.com/WebAssembly/wasi-filesystem/v0.2.3/wit/preopens.wit

cd ../../../..
```

**Step 2: Create world.wit**

Create `extras/fs-wrapper/wit/world.wit`:

```wit
package c2w:fs-wrapper@0.1.0;

world fs-provider {
    // Export the filesystem interfaces that Bochs imports
    export wasi:filesystem/types@0.2.3;
    export wasi:filesystem/preopens@0.2.3;
}
```

**Step 3: Verify wit files exist**

Run: `ls extras/fs-wrapper/wit/deps/*/`

Expected: io, clocks, filesystem directories with .wit files

**Step 4: Commit**

```bash
git add extras/fs-wrapper/wit/
git commit -m "feat(fs-wrapper): add WASI WIT dependencies"
```

---

### Task 3: Implement preopens interface

**Files:**
- Modify: `extras/fs-wrapper/src/lib.rs`

**Step 1: Add wit-bindgen generate and preopens implementation**

Replace `extras/fs-wrapper/src/lib.rs`:

```rust
#![allow(unused)]

wit_bindgen::generate!({
    world: "fs-provider",
    path: "wit",
});

use exports::wasi::filesystem::preopens::Guest as PreopensGuest;
use exports::wasi::filesystem::types::{
    Descriptor, DescriptorFlags, DescriptorStat, DescriptorType, DirectoryEntry,
    DirectoryEntryStream, ErrorCode, Filesize, Guest as TypesGuest, GuestDescriptor,
    GuestDirectoryEntryStream, MetadataHashValue, NewTimestamp, OpenFlags, PathFlags,
};
use wasi::io::streams::{InputStream, OutputStream};

// Embedded files - paths are relative to Cargo.toml
// These will be populated during Docker build
const BOCHSRC: &[u8] = include_bytes!("../files/bochsrc");
const BOOT_ISO: &[u8] = include_bytes!("../files/boot.iso");
const ROOTFS_BIN: &[u8] = include_bytes!("../files/rootfs.bin");

/// Virtual file entry
struct VirtualFile {
    name: &'static str,
    data: &'static [u8],
}

static FILES: &[VirtualFile] = &[
    VirtualFile { name: "bochsrc", data: BOCHSRC },
    VirtualFile { name: "boot.iso", data: BOOT_ISO },
    VirtualFile { name: "rootfs.bin", data: ROOTFS_BIN },
];

/// Find file index by name
fn find_file(name: &str) -> Option<usize> {
    let clean = name.trim_start_matches('/');
    FILES.iter().position(|f| f.name == clean)
}

// Preopens implementation
struct FsWrapper;

impl PreopensGuest for FsWrapper {
    fn get_directories() -> Vec<(Descriptor, String)> {
        // Return root directory descriptor (handle 0) mapped to /pack
        vec![(Descriptor::new(RootDescriptor), "/pack".into())]
    }
}

export!(FsWrapper);
```

**Step 2: Verify syntax compiles (will fail - types incomplete)**

Run: `cd extras/fs-wrapper && cargo check 2>&1 | head -20`

Expected: Errors about missing types (RootDescriptor, etc.) - this is expected

**Step 3: Commit partial progress**

```bash
git add extras/fs-wrapper/src/lib.rs
git commit -m "feat(fs-wrapper): implement preopens interface skeleton"
```

---

### Task 4: Implement descriptor types and stat functions

**Files:**
- Modify: `extras/fs-wrapper/src/lib.rs`

**Step 1: Add descriptor resource implementations**

Replace `extras/fs-wrapper/src/lib.rs` with full implementation:

```rust
#![allow(unused)]

wit_bindgen::generate!({
    world: "fs-provider",
    path: "wit",
});

use exports::wasi::filesystem::preopens::Guest as PreopensGuest;
use exports::wasi::filesystem::types::{
    Descriptor, DescriptorFlags, DescriptorStat, DescriptorType, DirectoryEntry,
    DirectoryEntryStream, ErrorCode, Filesize, Guest as TypesGuest, GuestDescriptor,
    GuestDirectoryEntryStream, MetadataHashValue, NewTimestamp, OpenFlags, PathFlags,
};
use wasi::io::streams::{InputStream, OutputStream};

// Embedded files - populated during Docker build
const BOCHSRC: &[u8] = include_bytes!("../files/bochsrc");
const BOOT_ISO: &[u8] = include_bytes!("../files/boot.iso");
const ROOTFS_BIN: &[u8] = include_bytes!("../files/rootfs.bin");

struct VirtualFile {
    name: &'static str,
    data: &'static [u8],
}

static FILES: &[VirtualFile] = &[
    VirtualFile { name: "bochsrc", data: BOCHSRC },
    VirtualFile { name: "boot.iso", data: BOOT_ISO },
    VirtualFile { name: "rootfs.bin", data: ROOTFS_BIN },
];

fn find_file(name: &str) -> Option<usize> {
    let clean = name.trim_start_matches('/');
    FILES.iter().position(|f| f.name == clean)
}

// Root directory descriptor
struct RootDescriptor;

impl GuestDescriptor for RootDescriptor {
    fn read_via_stream(&self, _offset: Filesize) -> Result<InputStream, ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn write_via_stream(&self, _offset: Filesize) -> Result<OutputStream, ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn append_via_stream(&self) -> Result<OutputStream, ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn advise(&self, _offset: Filesize, _length: Filesize, _advice: exports::wasi::filesystem::types::Advice) -> Result<(), ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn sync_data(&self) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn get_flags(&self) -> Result<DescriptorFlags, ErrorCode> {
        Ok(DescriptorFlags::READ)
    }

    fn get_type(&self) -> Result<DescriptorType, ErrorCode> {
        Ok(DescriptorType::Directory)
    }

    fn set_size(&self, _size: Filesize) -> Result<(), ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn set_times(&self, _atime: NewTimestamp, _mtime: NewTimestamp) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn read(&self, _length: Filesize, _offset: Filesize) -> Result<(Vec<u8>, bool), ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn write(&self, _buffer: Vec<u8>, _offset: Filesize) -> Result<Filesize, ErrorCode> {
        Err(ErrorCode::IsDirectory)
    }

    fn read_directory(&self) -> Result<DirectoryEntryStream, ErrorCode> {
        Ok(DirectoryEntryStream::new(RootDirStream { index: 0 }))
    }

    fn sync(&self) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn create_directory_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn stat(&self) -> Result<DescriptorStat, ErrorCode> {
        Ok(DescriptorStat {
            type_: DescriptorType::Directory,
            link_count: 1,
            size: 0,
            data_access_timestamp: None,
            data_modification_timestamp: None,
            status_change_timestamp: None,
        })
    }

    fn stat_at(&self, _flags: PathFlags, path: String) -> Result<DescriptorStat, ErrorCode> {
        match find_file(&path) {
            Some(idx) => Ok(DescriptorStat {
                type_: DescriptorType::RegularFile,
                link_count: 1,
                size: FILES[idx].data.len() as u64,
                data_access_timestamp: None,
                data_modification_timestamp: None,
                status_change_timestamp: None,
            }),
            None => Err(ErrorCode::NoEntry),
        }
    }

    fn set_times_at(&self, _flags: PathFlags, _path: String, _atime: NewTimestamp, _mtime: NewTimestamp) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn link_at(&self, _old_flags: PathFlags, _old_path: String, _new_desc: &Descriptor, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn open_at(&self, _flags: PathFlags, path: String, _open_flags: OpenFlags, _desc_flags: DescriptorFlags) -> Result<Descriptor, ErrorCode> {
        match find_file(&path) {
            Some(idx) => Ok(Descriptor::new(FileDescriptor { index: idx })),
            None => Err(ErrorCode::NoEntry),
        }
    }

    fn readlink_at(&self, _path: String) -> Result<String, ErrorCode> {
        Err(ErrorCode::Invalid)
    }

    fn remove_directory_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn rename_at(&self, _old_path: String, _new_desc: &Descriptor, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn symlink_at(&self, _old_path: String, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn unlink_file_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn is_same_object(&self, other: &Descriptor) -> bool {
        // Check if other is also root
        false // Simplified - would need proper handle comparison
    }

    fn metadata_hash(&self) -> Result<MetadataHashValue, ErrorCode> {
        Ok(MetadataHashValue { lower: 0, upper: 0 })
    }

    fn metadata_hash_at(&self, _flags: PathFlags, path: String) -> Result<MetadataHashValue, ErrorCode> {
        match find_file(&path) {
            Some(idx) => Ok(MetadataHashValue { lower: idx as u64, upper: 0 }),
            None => Err(ErrorCode::NoEntry),
        }
    }
}

// File descriptor for embedded files
struct FileDescriptor {
    index: usize,
}

impl GuestDescriptor for FileDescriptor {
    fn read_via_stream(&self, _offset: Filesize) -> Result<InputStream, ErrorCode> {
        Err(ErrorCode::Unsupported) // Use read() instead
    }

    fn write_via_stream(&self, _offset: Filesize) -> Result<OutputStream, ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn append_via_stream(&self) -> Result<OutputStream, ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn advise(&self, _offset: Filesize, _length: Filesize, _advice: exports::wasi::filesystem::types::Advice) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn sync_data(&self) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn get_flags(&self) -> Result<DescriptorFlags, ErrorCode> {
        Ok(DescriptorFlags::READ)
    }

    fn get_type(&self) -> Result<DescriptorType, ErrorCode> {
        Ok(DescriptorType::RegularFile)
    }

    fn set_size(&self, _size: Filesize) -> Result<(), ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn set_times(&self, _atime: NewTimestamp, _mtime: NewTimestamp) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn read(&self, length: Filesize, offset: Filesize) -> Result<(Vec<u8>, bool), ErrorCode> {
        let data = FILES[self.index].data;
        let offset = offset as usize;
        let length = length as usize;

        if offset >= data.len() {
            return Ok((vec![], true)); // EOF
        }

        let end = std::cmp::min(offset + length, data.len());
        let chunk = data[offset..end].to_vec();
        let at_eof = end >= data.len();

        Ok((chunk, at_eof))
    }

    fn write(&self, _buffer: Vec<u8>, _offset: Filesize) -> Result<Filesize, ErrorCode> {
        Err(ErrorCode::ReadOnly)
    }

    fn read_directory(&self) -> Result<DirectoryEntryStream, ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn sync(&self) -> Result<(), ErrorCode> {
        Ok(())
    }

    fn create_directory_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn stat(&self) -> Result<DescriptorStat, ErrorCode> {
        Ok(DescriptorStat {
            type_: DescriptorType::RegularFile,
            link_count: 1,
            size: FILES[self.index].data.len() as u64,
            data_access_timestamp: None,
            data_modification_timestamp: None,
            status_change_timestamp: None,
        })
    }

    fn stat_at(&self, _flags: PathFlags, _path: String) -> Result<DescriptorStat, ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn set_times_at(&self, _flags: PathFlags, _path: String, _atime: NewTimestamp, _mtime: NewTimestamp) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn link_at(&self, _old_flags: PathFlags, _old_path: String, _new_desc: &Descriptor, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn open_at(&self, _flags: PathFlags, _path: String, _open_flags: OpenFlags, _desc_flags: DescriptorFlags) -> Result<Descriptor, ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn readlink_at(&self, _path: String) -> Result<String, ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn remove_directory_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn rename_at(&self, _old_path: String, _new_desc: &Descriptor, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn symlink_at(&self, _old_path: String, _new_path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn unlink_file_at(&self, _path: String) -> Result<(), ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }

    fn is_same_object(&self, _other: &Descriptor) -> bool {
        false
    }

    fn metadata_hash(&self) -> Result<MetadataHashValue, ErrorCode> {
        Ok(MetadataHashValue { lower: self.index as u64, upper: 1 })
    }

    fn metadata_hash_at(&self, _flags: PathFlags, _path: String) -> Result<MetadataHashValue, ErrorCode> {
        Err(ErrorCode::NotDirectory)
    }
}

// Directory entry stream for listing /pack contents
struct RootDirStream {
    index: usize,
}

impl GuestDirectoryEntryStream for RootDirStream {
    fn read_directory_entry(&self) -> Result<Option<DirectoryEntry>, ErrorCode> {
        if self.index >= FILES.len() {
            Ok(None)
        } else {
            Ok(Some(DirectoryEntry {
                type_: DescriptorType::RegularFile,
                name: FILES[self.index].name.to_string(),
            }))
        }
    }
}

// Types interface (no methods, just types)
struct FsTypes;

impl TypesGuest for FsTypes {
    type Descriptor = Box<dyn GuestDescriptor>;
    type DirectoryEntryStream = RootDirStream;
}

// Preopens interface
struct FsWrapper;

impl PreopensGuest for FsWrapper {
    fn get_directories() -> Vec<(Descriptor, String)> {
        vec![(Descriptor::new(RootDescriptor), "/pack".into())]
    }
}

export!(FsWrapper with_types_in exports);
```

**Step 2: Create placeholder files directory**

```bash
mkdir -p extras/fs-wrapper/files
echo "placeholder" > extras/fs-wrapper/files/bochsrc
dd if=/dev/zero of=extras/fs-wrapper/files/boot.iso bs=1 count=1
dd if=/dev/zero of=extras/fs-wrapper/files/rootfs.bin bs=1 count=1
```

**Step 3: Check compilation**

Run: `cd extras/fs-wrapper && cargo check 2>&1`

Note: This will likely have compilation errors due to wit-bindgen specifics. We'll iterate on the exact types.

**Step 4: Commit progress**

```bash
git add extras/fs-wrapper/
git commit -m "feat(fs-wrapper): implement descriptor and file types"
```

---

### Task 5: Debug and fix wit-bindgen compilation

**Files:**
- Modify: `extras/fs-wrapper/src/lib.rs`

**Step 1: Install cargo-component locally for testing**

```bash
cargo install cargo-component --locked
```

**Step 2: Run cargo component build to get exact errors**

Run: `cd extras/fs-wrapper && cargo component build 2>&1`

**Step 3: Fix type mismatches based on actual wit-bindgen output**

The exact fixes will depend on the error messages. Common issues:
- Type names differ between wit and generated Rust
- Export macro syntax varies by wit-bindgen version
- Resource trait implementations may need adjustment

Iterate until: `cargo component build` succeeds

**Step 4: Commit working implementation**

```bash
git add extras/fs-wrapper/src/lib.rs
git commit -m "fix(fs-wrapper): resolve wit-bindgen type issues"
```

---

### Task 6: Add fs-wrapper build stage to Dockerfile

**Files:**
- Modify: `Dockerfile` (after line 1080, before bochs-dev-p2-common)

**Step 1: Add fs-wrapper-build stage**

Insert after line 1080 (after bochs-toolchain-p2 stage):

```dockerfile
# ===== FILESYSTEM WRAPPER COMPONENT =====
FROM rust:1.85.0-bookworm AS fs-wrapper-build
WORKDIR /work

# Install cargo-component for building WASM components
RUN cargo install cargo-component --locked

# Install wac-cli for component composition
RUN cargo install wac-cli --locked

# Copy the fs-wrapper source
COPY --link extras/fs-wrapper /work/fs-wrapper

# Will be populated with actual files later by composition stage
```

**Step 2: Verify Dockerfile syntax**

Run: `docker build --target fs-wrapper-build -f Dockerfile . 2>&1 | tail -20`

Expected: Build should start (may fail on missing files, that's OK)

**Step 3: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add fs-wrapper-build stage"
```

---

### Task 7: Add fs-wrapper composition stage

**Files:**
- Modify: `Dockerfile` (replace bochs-dev-p2-packed stage, lines 1126-1134)

**Step 1: Create new fs-wrapper-with-files stage**

Insert before bochs-dev-p2-packed:

```dockerfile
# Build fs-wrapper with embedded VM files
FROM fs-wrapper-build AS fs-wrapper-with-files
# Copy actual VM files from vm-amd64-dev
COPY --link --from=vm-amd64-dev /pack/bochsrc /work/fs-wrapper/files/bochsrc
COPY --link --from=vm-amd64-dev /pack/boot.iso /work/fs-wrapper/files/boot.iso
COPY --link --from=vm-amd64-dev /pack/rootfs.bin /work/fs-wrapper/files/rootfs.bin
# Build the component
WORKDIR /work/fs-wrapper
RUN cargo component build --release
# Output: /work/fs-wrapper/target/wasm32-wasip2/release/fs_wrapper.wasm
```

**Step 2: Modify bochs-dev-p2-packed to compose components**

Replace lines 1126-1134 with:

```dockerfile
FROM bochs-dev-p2-${OPTIMIZATION_MODE} AS bochs-dev-p2-packed
# Convert core wasm to component with preview1 adapter
RUN /tools/wasm-tools/wasm-tools component new bochs \
    --adapt wasi_snapshot_preview1=/tools/wasi_snapshot_preview1.reactor.wasm \
    -o bochs.component.wasm

# Copy fs-wrapper component and wac tool
COPY --link --from=fs-wrapper-with-files /work/fs-wrapper/target/wasm32-wasip2/release/fs_wrapper.wasm /
COPY --link --from=fs-wrapper-with-files /usr/local/cargo/bin/wac /usr/local/bin/wac

# Compose: plug fs-wrapper into bochs to satisfy filesystem imports
RUN wac plug bochs.component.wasm --plug fs_wrapper.wasm -o bochs.composed.wasm

# Package final output (single file, no external filesystem needed)
RUN mkdir /out
ARG OUTPUT_NAME
RUN mv bochs.composed.wasm /out/$OUTPUT_NAME
```

**Step 3: Verify Dockerfile syntax**

Run: `docker build --check -f Dockerfile . 2>&1 || echo "Syntax check done"`

**Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat(dockerfile): add fs-wrapper composition for wasip2"
```

---

### Task 8: Test full wasip2 build pipeline

**Files:**
- Test: Full Docker build with `--target=wasi-p2`

**Step 1: Build the c2w binary**

```bash
go build -o c2w ./cmd/c2w
```

**Step 2: Run full wasip2 build**

```bash
./c2w --target=wasi-p2 --build-arg OPTIMIZATION_MODE=native alpine:latest test-composed.wasm
```

Expected: Build completes, outputs single `test-composed.wasm` file

**Step 3: Verify no filesystem imports in output**

```bash
wasm-tools component wit test-composed.wasm 2>/dev/null | grep -c "import wasi:filesystem" || echo "0 filesystem imports (good!)"
```

Expected: 0 (all filesystem imports satisfied by wrapper)

**Step 4: Test with wasmtime**

```bash
wasmtime run test-composed.wasm
```

Expected: VM starts booting (Bochs output visible)

**Step 5: Commit test artifacts to .gitignore**

```bash
echo "test-composed.wasm" >> .gitignore
git add .gitignore
git commit -m "chore: add test artifacts to gitignore"
```

---

### Task 9: Update documentation

**Files:**
- Modify: `README.md` (wasip2 section)
- Modify: `docs/plans/2026-01-26-wasip2-support-impl.md`

**Step 1: Update README wasip2 section**

Find the wasip2 documentation section and update to reflect single-file output:

```markdown
### WASI Preview 2 (wasip2) Support

The `--target=wasi-p2` flag generates a Component Model WASM file with embedded filesystem:

```bash
c2w --target=wasi-p2 alpine:latest out.wasm
wasmtime run out.wasm
```

The wasip2 output is a self-contained single file - no external filesystem mounting required.
```

**Step 2: Commit documentation**

```bash
git add README.md docs/
git commit -m "docs: update wasip2 to reflect single-file output"
```

---

### Task 10: Update upstream PR

**Files:**
- None (git/gh operations only)

**Step 1: Push changes to feature branch**

```bash
git push origin feature/wasip2-support
```

**Step 2: Update PR description**

Add comment to PR #565 noting the fs-wrapper implementation:

```bash
gh pr comment 565 --repo ktock/container2wasm --body "Updated: Implemented filesystem wrapper component using wac composition. The wasip2 output is now a single self-contained file with embedded filesystem - no external file mounting required."
```

**Step 3: Verify PR status**

```bash
gh pr view 565 --repo ktock/container2wasm
```

Expected: PR shows recent commits with fs-wrapper changes

---

## Implementation Status

**COMPLETED: 2026-01-26**

All tasks completed. WASI P2 standalone operation working.

### Key Implementation Notes

1. **fs-wrapper Component**: Implements `wasi:filesystem/types` and `wasi:filesystem/preopens` interfaces to provide embedded files (boot.iso, rootfs.bin, wasi2-config) to Bochs.

2. **vmtouch Pre-caching**: The fs-wrapper's `read_via_stream` is not fully implemented (would require exporting `wasi:io/streams`). Workaround: use vmtouch during wizer to cache the entire container rootfs into the kernel page cache before snapshot. This increases wasm size (~8MB for Alpine) but ensures all file data is available at runtime.

3. **Build Pipeline**:
   - Bochs compiled with wasip1, converted to component via `wasm-tools component new`
   - fs-wrapper compiled as component with embedded files via `cargo-component`
   - Components composed via `wac plug` to satisfy Bochs' filesystem imports

### Verification Commands

```bash
# Build wasip2 output
./c2w --target wasi-p2 --assets . --dockerfile Dockerfile alpine:3.19 out.wasm

# Run standalone (no filesystem mounting needed)
echo 'echo hello; exit' | wasmtime run out.wasm

# Check component structure
wasm-tools component wit out.wasm | head -30

# Regression test (wasip1 still works)
./c2w --target wasi-p1 --assets . --dockerfile Dockerfile alpine:3.19 out-p1.wasm
echo 'echo hello; exit' | wasmtime run --dir /::/ out-p1.wasm
```

### File Sizes

- WASI P1 output: ~108 MB (uses wasi-vfs)
- WASI P2 output: ~117 MB (includes vmtouch-cached rootfs)

### Known Limitations

1. `read_via_stream` not implemented - relies on vmtouch pre-caching
2. Larger binary due to embedded rootfs cache
3. External bundle mode not yet tested with wasip2
