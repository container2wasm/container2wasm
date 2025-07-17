# syntax = docker/dockerfile:1.5

ARG BINFMT_VERSION=qemu-v6.1.0
ARG WASI_SDK_VERSION=19
ARG WASI_SDK_VERSION_FULL=${WASI_SDK_VERSION}.0
ARG WASI_VFS_VERSION=v0.3.0
ARG WIZER_VERSION=04e49c989542f2bf3a112d60fbf88a62cce2d0d0
ARG EMSDK_VERSION=3.1.40 # TODO: support recent version
ARG EMSDK_VERSION_QEMU=3.1.50 # TODO: support recent version
ARG BINARYEN_VERSION=114
ARG BUSYBOX_VERSION=1.36.1
ARG RUNC_VERSION=v1.2.5

# ARG LINUX_LOGLEVEL=0
# ARG INIT_DEBUG=false
ARG LINUX_LOGLEVEL=7
ARG INIT_DEBUG=true
ARG VM_MEMORY_SIZE_MB=128
ARG VM_CORE_NUMS=1
ARG QEMU_MIGRATION=true
ARG NO_VMTOUCH=
ARG EXTERNAL_BUNDLE=
ARG NO_BINFMT=

ARG LOAD_MODE=single # or separated

ARG OUTPUT_NAME=out.wasm # for wasi
ARG JS_OUTPUT_NAME=out # for emscripten; must not include "."
ARG OPTIMIZATION_MODE=wizer # "wizer" or "native"
# ARG OPTIMIZATION_MODE=native

ARG TINYEMU_REPO=https://github.com/ktock/tinyemu-c2w
ARG TINYEMU_REPO_VERSION=e4e9bd198f9c0505ab4c77a6a9d038059cd1474a

ARG BOCHS_REPO=https://github.com/ktock/Bochs
ARG BOCHS_REPO_VERSION=a88d1f687ec83ff82b5318f59dcecb8dab44fc83

ARG QEMU_REPO=https://github.com/ktock/qemu-wasm
ARG QEMU_REPO_VERSION=8604ed49a3cde392890b014a8d5a959c8a2fe72a

ARG SOURCE_REPO=https://github.com/ktock/container2wasm
ARG SOURCE_REPO_VERSION=v0.8.2

ARG ZLIB_VERSION=1.3.1
ARG GLIB_MINOR_VERSION=2.75
ARG GLIB_VERSION=${GLIB_MINOR_VERSION}.0
ARG PIXMAN_VERSION=0.42.2
ARG FFI_VERSION=adbcf2b247696dde2667ab552cb93e0c79455c84

FROM scratch AS oci-image-src
COPY . .

FROM ubuntu:22.04 AS assets-base
ARG SOURCE_REPO
ARG SOURCE_REPO_VERSION
RUN apt-get update && apt-get install -y git
RUN git clone -b ${SOURCE_REPO_VERSION} ${SOURCE_REPO} /assets
FROM scratch AS assets
COPY --link --from=assets-base /assets /

FROM ubuntu:22.04 AS tinyemu-repo-base
ARG TINYEMU_REPO
ARG TINYEMU_REPO_VERSION
RUN apt-get update && apt-get install -y git
RUN git clone ${TINYEMU_REPO} /tinyemu && \
    cd /tinyemu && \
    git checkout ${TINYEMU_REPO_VERSION}
FROM scratch AS tinyemu-repo
COPY --link --from=tinyemu-repo-base /tinyemu /

FROM ubuntu:22.04 AS bochs-repo-base
ARG BOCHS_REPO
ARG BOCHS_REPO_VERSION
RUN apt-get update && apt-get install -y git
RUN git clone ${BOCHS_REPO} /Bochs && \
    cd /Bochs && \
    git checkout ${BOCHS_REPO_VERSION}
FROM scratch AS bochs-repo
COPY --link --from=bochs-repo-base /Bochs /

FROM ubuntu:22.04 AS qemu-repo-base
ARG QEMU_REPO
ARG QEMU_REPO_VERSION
RUN apt-get update && apt-get install -y git
RUN git clone --depth 100 ${QEMU_REPO} /qemu && \
    cd /qemu && \
    git checkout ${QEMU_REPO_VERSION}
FROM scratch AS qemu-repo
COPY --link --from=qemu-repo-base /qemu /

FROM golang:1.24-bullseye AS golang-base

FROM golang-base AS bundle-dev
ARG TARGETPLATFORM
ARG INIT_DEBUG
ARG OPTIMIZATION_MODE
ARG NO_VMTOUCH
ARG NO_BINFMT
ARG EXTERNAL_BUNDLE
COPY --link --from=assets / /work
WORKDIR /work
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -o /bin/create-spec ./cmd/create-spec
COPY --link --from=oci-image-src / /oci
# This step creates the following files
# <vm-rootfs>/oci/rootfs          : rootfs dir this Dockerfile creates container's rootfs and used by the container.
# <vm-rootfs>/oci/image.json      : container image config file used by init
# <vm-rootfs>/oci/spec.json       : container runtime spec file used by init
# <vm-rootfs>/oci/initconfig.json : configuration file for init
RUN mkdir -p /out/oci/rootfs /out/oci/bundle && \
    IS_WIZER=false && \
    if test "${OPTIMIZATION_MODE}" = "wizer" ; then IS_WIZER=true ; fi && \
    NO_VMTOUCH_F=false && \
    NO_BINFMT_F=false && \
    if test "${OPTIMIZATION_MODE}" = "native" ; then NO_VMTOUCH_F=true ; NO_BINFMT_F=true ; fi && \
    if test "${NO_VMTOUCH}" != "" ; then NO_VMTOUCH_F="${NO_VMTOUCH}" ; fi && \
    if test "${NO_BINFMT}" != "" ; then NO_BINFMT_F="${NO_BINFMT}" ; fi && \
    EXTERNAL_BUNDLE_F=false && \
    if test "${EXTERNAL_BUNDLE}" = "true" ; then EXTERNAL_BUNDLE_F=true ; fi && \
    create-spec --debug=${INIT_DEBUG} --debug-init=${IS_WIZER} --no-vmtouch=${NO_VMTOUCH_F} --external-bundle=${EXTERNAL_BUNDLE_F} --no-binfmt=${NO_BINFMT_F} \
                --image-config-path=/oci/image.json \
                --runtime-config-path=/oci/spec.json \
                --rootfs-path=/oci/rootfs \
                /oci "${TARGETPLATFORM}" /out/oci/rootfs
RUN if test -f image.json; then mv image.json /out/oci/ ; fi && \
    if test -f spec.json; then mv spec.json /out/oci/ ; fi
RUN mv initconfig.json /out/oci/

FROM ubuntu:22.04 AS gcc-riscv64-linux-gnu-base
RUN apt-get update && apt-get install -y gcc-riscv64-linux-gnu libc-dev-riscv64-cross git make

FROM gcc-riscv64-linux-gnu-base AS bbl-dev
WORKDIR /work-buildroot/
RUN git clone https://github.com/riscv-software-src/riscv-pk
WORKDIR /work-buildroot/riscv-pk
RUN git checkout 7e9b671c0415dfd7b562ac934feb9380075d4aa2
RUN mkdir build
WORKDIR /work-buildroot/riscv-pk/build
RUN ../configure --host=riscv64-linux-gnu
RUN cat ../machine/htif.c ../bbl/bbl.lds
# HTIF address needs to be static on TinyEMU
RUN sed -i 's/volatile uint64_t tohost __attribute__((section(".htif")));/#define tohost *(uint64_t*)0x40008000/' ../machine/htif.c && \
    sed -i 's/volatile uint64_t fromhost __attribute__((section(".htif")));/#define fromhost *(uint64_t*)0x40008008/' ../machine/htif.c
RUN make bbl
RUN riscv64-linux-gnu-objcopy -O binary bbl bbl.bin && \
    mkdir /out/ && \
    mv bbl.bin /out/

FROM gcc-riscv64-linux-gnu-base AS linux-riscv64-dev-common
RUN apt-get update && apt-get install -y gperf flex bison bc
RUN mkdir /work-buildlinux
WORKDIR /work-buildlinux
RUN git clone -b v6.1 --depth 1 https://github.com/torvalds/linux

FROM linux-riscv64-dev-common AS linux-riscv64-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets /config/tinyemu/linux_rv64_config ./.config
RUN make ARCH=riscv CROSS_COMPILE=riscv64-linux-gnu- -j$(nproc) all && \
    mkdir /out && \
    mv /work-buildlinux/linux/arch/riscv/boot/Image /out/Image && \
    make clean

FROM linux-riscv64-dev-common AS linux-riscv64-config-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets /config/tinyemu/linux_rv64_config ./.config
RUN make ARCH=riscv CROSS_COMPILE=riscv64-linux-gnu- olddefconfig

FROM scratch AS linux-riscv64-config
COPY --link --from=linux-riscv64-config-dev /work-buildlinux/linux/.config /

FROM golang-base AS init-riscv64-dev
COPY --link --from=assets / /work
WORKDIR /work
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOARCH=riscv64 go build -ldflags "-s -w -extldflags '-static'" -tags "osusergo netgo static_build" -o /out/init ./cmd/init

FROM golang-base AS runc-riscv64-dev
ARG RUNC_VERSION
RUN apt-get update -y && apt-get install -y gcc-riscv64-linux-gnu libc-dev-riscv64-cross git make gperf
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc && \
    cd /go/src/github.com/opencontainers/runc && \
    git checkout "${RUNC_VERSION}" && \
    # mkdir -p /opt/libseccomp && ./script/seccomp.sh "2.5.4" /opt/libseccomp riscv64 && \
    make static GOARCH=riscv64 CC=riscv64-linux-gnu-gcc EXTRA_LDFLAGS='-s -w' BUILDTAGS="" && \
    mkdir -p /out/ && mv runc /out/runc

FROM gcc-riscv64-linux-gnu-base AS vmtouch-riscv64-dev
RUN git clone https://github.com/hoytech/vmtouch.git && \
    cd vmtouch && \
    CC="riscv64-linux-gnu-gcc -static" make && \
    mkdir /out && mv vmtouch /out/

FROM --platform=riscv64 tonistiigi/binfmt:${BINFMT_VERSION} AS binfmt
FROM scratch AS binfmt-riscv64
FROM scratch AS binfmt-base
COPY --link --from=binfmt /usr/bin/binfmt /usr/bin/
FROM binfmt-base AS binfmt-amd64
COPY --link --from=binfmt /usr/bin/qemu-x86_64 /usr/bin/
FROM binfmt-base AS binfmt-aarch64
COPY --link --from=binfmt /usr/bin/qemu-aarch64 /usr/bin/
FROM binfmt-base AS binfmt-arm
COPY --link --from=binfmt /usr/bin/qemu-arm /usr/bin/
FROM binfmt-base AS binfmt-i386
COPY --link --from=binfmt /usr/bin/qemu-i386 /usr/bin/
FROM binfmt-base AS binfmt-mips64
COPY --link --from=binfmt /usr/bin/qemu-mips64 /usr/bin/
FROM binfmt-base AS binfmt-ppc64le
COPY --link --from=binfmt /usr/bin/qemu-ppc64le /usr/bin/
FROM binfmt-base AS binfmt-s390
COPY --link --from=binfmt /usr/bin/qemu-s390 /usr/bin/
FROM binfmt-$TARGETARCH AS binfmt-dev

FROM gcc-riscv64-linux-gnu-base AS busybox-riscv64-dev
ARG BUSYBOX_VERSION
RUN apt-get update -y && apt-get install -y gcc bzip2 wget
WORKDIR /work
RUN wget https://busybox.net/downloads/busybox-${BUSYBOX_VERSION}.tar.bz2
RUN bzip2 -d busybox-${BUSYBOX_VERSION}.tar.bz2
RUN tar xvf busybox-${BUSYBOX_VERSION}.tar
WORKDIR /work/busybox-${BUSYBOX_VERSION}
RUN make CROSS_COMPILE=riscv64-linux-gnu- LDFLAGS=--static defconfig
RUN make CROSS_COMPILE=riscv64-linux-gnu- LDFLAGS=--static -j$(nproc)
RUN mkdir -p /out/bin && mv busybox /out/bin/busybox
RUN make LDFLAGS=--static defconfig
RUN make LDFLAGS=--static -j$(nproc)
RUN for i in $(./busybox --list) ; do ln -s busybox /out/bin/$i ; done
RUN mkdir -p /out/usr/share/udhcpc/ && cp ./examples/udhcp/simple.script /out/usr/share/udhcpc/default.script

FROM gcc-riscv64-linux-gnu-base AS tini-riscv64-dev
# https://github.com/krallin/tini#building-tini
RUN apt-get update -y && apt-get install -y cmake
ENV CFLAGS="-DPR_SET_CHILD_SUBREAPER=36 -DPR_GET_CHILD_SUBREAPER=37"
WORKDIR /work
RUN git clone -b v0.19.0 https://github.com/krallin/tini
WORKDIR /work/tini
ENV CC="riscv64-linux-gnu-gcc -static"
RUN cmake . && make && mkdir /out/ && mv tini /out/

FROM ubuntu:22.04 AS rootfs-riscv64-dev
RUN apt-get update -y && apt-get install -y mkisofs
COPY --link --from=busybox-riscv64-dev /out/ /rootfs/
COPY --link --from=binfmt-dev / /rootfs/
COPY --link --from=runc-riscv64-dev /out/runc /rootfs/sbin/runc
COPY --link --from=bundle-dev /out/ /rootfs/
COPY --link --from=init-riscv64-dev /out/init /rootfs/sbin/init
COPY --link --from=vmtouch-riscv64-dev /out/vmtouch /rootfs/bin/
COPY --link --from=tini-riscv64-dev /out/tini /rootfs/sbin/tini
RUN mkdir -p /rootfs/proc /rootfs/sys /rootfs/mnt /rootfs/run /rootfs/tmp /rootfs/dev /rootfs/var /rootfs/etc && mknod /rootfs/dev/null c 1 3 && chmod 666 /rootfs/dev/null
RUN mkdir /out/ && mkisofs -R -o /out/rootfs.bin /rootfs/
# RUN isoinfo -i /out/rootfs.bin -l

FROM ubuntu:22.04 AS tinyemu-config-dev
ARG LINUX_LOGLEVEL
ARG VM_MEMORY_SIZE_MB
RUN apt-get update && apt-get install -y gettext-base && mkdir /out
COPY --link --from=assets /config/tinyemu/tinyemu.config.template /
RUN cat /tinyemu.config.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB envsubst > /out/tinyemu.config

FROM scratch AS vm-riscv64-dev
COPY --link --from=bbl-dev /out/bbl.bin /pack/bbl.bin
COPY --link --from=linux-riscv64-dev /out/Image /pack/Image
COPY --link --from=rootfs-riscv64-dev /out/rootfs.bin /pack/rootfs.bin
COPY --link --from=tinyemu-config-dev /out/tinyemu.config /pack/config

FROM rust:1.74.1-bullseye AS tinyemu-dev-common
ARG WASI_VFS_VERSION
ARG WASI_SDK_VERSION
ARG WASI_SDK_VERSION_FULL
ARG WIZER_VERSION
RUN apt-get update -y && apt-get install -y make curl git gcc xz-utils

WORKDIR /wasi
RUN curl -o wasi-sdk.tar.gz -fSL https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION}/wasi-sdk-${WASI_SDK_VERSION_FULL}-linux.tar.gz && \
    tar xvf wasi-sdk.tar.gz && rm wasi-sdk.tar.gz
ENV WASI_SDK_PATH=/wasi/wasi-sdk-${WASI_SDK_VERSION_FULL}

WORKDIR /work/
RUN git clone https://github.com/kateinoigakukun/wasi-vfs.git --recurse-submodules && \
    cd wasi-vfs && \
    git checkout "${WASI_VFS_VERSION}" && \
    cargo build --target wasm32-unknown-unknown && \
    cargo build --package wasi-vfs-cli && \
    mkdir -p /tools/wasi-vfs/ && \
    mv target/debug/wasi-vfs target/wasm32-unknown-unknown/debug/libwasi_vfs.a /tools/wasi-vfs/ && \
    cargo clean

WORKDIR /work/
RUN git clone https://github.com/bytecodealliance/wizer && \
    cd wizer && \
    git checkout "${WIZER_VERSION}" && \
    cargo build --bin wizer --all-features && \
    mkdir -p /tools/wizer/ && \
    mv include target/debug/wizer /tools/wizer/ && \
    cargo clean

COPY --link --from=tinyemu-repo / /tinyemu
WORKDIR /tinyemu
RUN make -j $(nproc) -f Makefile \
    CONFIG_FS_NET= CONFIG_SDL= CONFIG_INT128= CONFIG_X86EMU= CONFIG_SLIRP= \
    CC="${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -D_WASI_EMULATED_SIGNAL -DWASI -I/tools/wizer/include/" \
    EMU_LIBS="/tools/wasi-vfs/libwasi_vfs.a -lrt" \
    EMU_OBJS="virtio.o pci.o fs.o cutils.o iomem.o simplefb.o json.o machine.o temu.o wasi.o riscv_machine.o softfp.o riscv_cpu32.o riscv_cpu64.o fs_disk.o"

FROM tinyemu-dev-common AS tinyemu-dev-native
COPY --link --from=vm-riscv64-dev /pack /minpack

FROM tinyemu-dev-common AS tinyemu-dev-wizer
COPY --link --from=vm-riscv64-dev /pack /pack
RUN mv temu temu-org && /tools/wizer/wizer --allow-wasi --wasm-bulk-memory=true -r _start=wizer.resume --mapdir /pack::/pack -o temu temu-org
RUN mkdir /minpack && cp /pack/rootfs.bin /minpack/

FROM tinyemu-dev-${OPTIMIZATION_MODE} AS tinyemu-dev-packed
RUN /tools/wasi-vfs/wasi-vfs pack /tinyemu/temu --mapdir /pack::/minpack -o packed && mkdir /out
ARG OUTPUT_NAME
RUN mv packed /out/$OUTPUT_NAME

FROM emscripten/emsdk:$EMSDK_VERSION AS tinyemu-emscripten
ARG JS_OUTPUT_NAME
RUN apt-get update && apt-get install -y git
COPY --link --from=tinyemu-repo / /tinyemu
COPY --link --from=vm-riscv64-dev /pack /pack
WORKDIR /tinyemu
RUN make -j $(nproc) -f Makefile \
    CONFIG_FS_NET= CONFIG_SDL= CONFIG_INT128= CONFIG_X86EMU= CONFIG_SLIRP= OUTPUT_NAME=$JS_OUTPUT_NAME \
    CC="emcc --preload-file /pack -s WASM=1 -s ASYNCIFY=1 -s ALLOW_MEMORY_GROWTH=1 -UEMSCRIPTEN -DON_BROWSER -sNO_EXIT_RUNTIME=1 -sFORCE_FILESYSTEM=1" && \
    mkdir -p /out/ && mv ${JS_OUTPUT_NAME} /out/${JS_OUTPUT_NAME}.js && mv ${JS_OUTPUT_NAME}.wasm /out/ && mv ${JS_OUTPUT_NAME}.data /out/

FROM scratch AS js-tinyemu
COPY --link --from=tinyemu-emscripten /out/ /
FROM js-tinyemu AS js-arm
FROM js-tinyemu AS js-i386
FROM js-tinyemu AS js-mips64
FROM js-tinyemu AS js-ppc64le
FROM js-tinyemu AS js-s390

FROM scratch AS wasi-tinyemu
COPY --link --from=tinyemu-dev-packed /out/ /
FROM wasi-tinyemu AS wasi-riscv64
FROM wasi-tinyemu AS wasi-aarch64
FROM wasi-tinyemu AS wasi-arm
FROM wasi-tinyemu AS wasi-i386
FROM wasi-tinyemu AS wasi-mips64
FROM wasi-tinyemu AS wasi-ppc64le
FROM wasi-tinyemu AS wasi-s390

FROM emscripten/emsdk:$EMSDK_VERSION_QEMU AS glib-emscripten-base
# Porting glib to emscripten inspired by https://github.com/emscripten-core/emscripten/issues/11066
ENV TARGET=/glib-emscripten/target
ENV CFLAGS="-O2 -matomics -mbulk-memory -DNDEBUG -sWASM_BIGINT -DWASM_BIGINT -pthread -sMALLOC=emmalloc  -sASYNCIFY=1 "
ENV CXXFLAGS="$CFLAGS"
ENV LDFLAGS="-L$TARGET/lib -O2"
ENV CPATH="$TARGET/include"
ENV PKG_CONFIG_PATH="$TARGET/lib/pkgconfig"
ENV EM_PKG_CONFIG_PATH="$PKG_CONFIG_PATH"
ENV CHOST="wasm32-unknown-linux"
ENV MAKEFLAGS="-j$(nproc)"
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y \
    autoconf \
    build-essential \
    libglib2.0-dev \
    libtool \
    pkgconf \
    ninja-build \
    pipx
RUN PIPX_BIN_DIR=/usr/local/bin pipx install meson==1.5.0
RUN mkdir /glib-emscripten
WORKDIR /glib-emscripten
RUN mkdir -p $TARGET

FROM glib-emscripten-base AS zlib-emscripten-dev
ARG ZLIB_VERSION
RUN mkdir -p /zlib
RUN curl -Ls https://zlib.net/zlib-$ZLIB_VERSION.tar.xz | tar xJC /zlib --strip-components=1
WORKDIR /zlib
RUN emconfigure ./configure --prefix=$TARGET --static
RUN make install

FROM glib-emscripten-base AS libffi-emscripten-dev
ARG FFI_VERSION
RUN mkdir -p /libffi
RUN git clone https://github.com/libffi/libffi /libffi
WORKDIR /libffi
RUN git checkout $FFI_VERSION
RUN autoreconf -fiv
RUN LDFLAGS="$LDFLAGS -sEXPORTED_RUNTIME_METHODS='getTempRet0,setTempRet0'" ; \
    emconfigure ./configure --host=$CHOST --prefix=$TARGET --enable-static --disable-shared --disable-dependency-tracking \
    --disable-builddir --disable-multi-os-directory --disable-raw-api --disable-structs --disable-docs
RUN emmake make install SUBDIRS='include'

FROM glib-emscripten-base AS glib-emscripten-dev
ARG GLIB_VERSION
ARG GLIB_MINOR_VERSION
RUN mkdir -p /stub
WORKDIR /stub
RUN <<EOF
cat <<'EOT' > res_query.c
#include <netdb.h>
int res_query(const char *name, int class, int type, unsigned char *dest, int len)
{
    h_errno = HOST_NOT_FOUND;
    return -1;
}
EOT
EOF
RUN emcc ${CFLAGS} -c res_query.c -fPIC -o libresolv.o
RUN ar rcs libresolv.a libresolv.o
RUN mkdir -p $TARGET/lib/
RUN cp libresolv.a $TARGET/lib/

RUN mkdir -p /glib
RUN curl -Lks https://download.gnome.org/sources/glib/${GLIB_MINOR_VERSION}/glib-$GLIB_VERSION.tar.xz | tar xJC /glib --strip-components=1

COPY --link --from=zlib-emscripten-dev /glib-emscripten/ /glib-emscripten/
COPY --link --from=libffi-emscripten-dev /glib-emscripten/ /glib-emscripten/

WORKDIR /glib
ENV CFLAGS="-Wno-error=incompatible-function-pointer-types -Wincompatible-function-pointer-types -O2 -matomics -mbulk-memory -DNDEBUG -pthread -sWASM_BIGINT -sMALLOC=emmalloc -sASYNCIFY=1"
ENV CXXFLAGS="$CFLAGS"
RUN <<EOF
cat <<'EOT' > /emcc-meson-wrap.sh
#!/bin/bash
set -euo pipefail
old_string="-Werror=unused-command-line-argument"
# emscripten ignores some -s flags during compilation with warnings. Meson checking phase fails when it sees these warnings.
new_string="-Wno-error=unused-command-line-argument"
cmd="$1"
shift
new_args=()
for arg in "$@"; do
  new_arg="${arg//$old_string/$new_string}"
  new_args+=("$new_arg")
done
"$cmd" "${new_args[@]}"
EOT
EOF
RUN <<EOF
cat <<'EOT' > /cross.meson
[host_machine]
system = 'emscripten'
cpu_family = 'wasm32'
cpu = 'wasm32'
endian = 'little'

[binaries]
c = ['bash', '/emcc-meson-wrap.sh', 'emcc']
cpp = ['bash', '/emcc-meson-wrap.sh', 'em++']
ar = 'emar'
ranlib = 'emranlib'
pkgconfig = ['pkg-config', '--static']
EOT
EOF
RUN meson setup _build --prefix=$TARGET --cross-file=/cross.meson --default-library=static --buildtype=release \
    --force-fallback-for=pcre2,gvdb -Dselinux=disabled -Dxattr=false -Dlibmount=disabled -Dnls=disabled \
    -Dtests=false -Dglib_assert=false -Dglib_checks=false
RUN sed -i -E "/#define HAVE_CLOSE_RANGE 1/d" ./_build/config.h
RUN sed -i -E "/#define HAVE_EPOLL_CREATE 1/d" ./_build/config.h
RUN sed -i -E "/#define HAVE_KQUEUE 1/d" ./_build/config.h
RUN sed -i -E "/#define HAVE_POSIX_SPAWN 1/d" ./_build/config.h
RUN sed -i -E "/#define HAVE_FALLOCATE 1/d" ./_build/config.h
RUN meson install -C _build

FROM glib-emscripten-base AS pixman-emscripten-dev
ARG PIXMAN_VERSION
RUN mkdir /pixman/
RUN git clone  https://gitlab.freedesktop.org/pixman/pixman /pixman/
WORKDIR /pixman
RUN git checkout pixman-$PIXMAN_VERSION
RUN NOCONFIGURE=y ./autogen.sh
RUN emconfigure ./configure --prefix=/glib-emscripten/target/
RUN emmake make -j$(nproc)
RUN emmake make install
RUN rm /glib-emscripten/target/lib/libpixman-1.so /glib-emscripten/target/lib/libpixman-1.so.0 /glib-emscripten/target/lib/libpixman-1.so.$PIXMAN_VERSION

FROM ubuntu:22.04 AS gcc-x86-64-linux-gnu-base
RUN apt-get update && apt-get install -y gcc-x86-64-linux-gnu linux-libc-dev-amd64-cross git make

FROM gcc-x86-64-linux-gnu-base AS linux-amd64-dev-common
RUN apt-get update && apt-get install -y gperf flex bison bc
RUN mkdir /work-buildlinux
WORKDIR /work-buildlinux
RUN git clone -b v6.1 --depth 1 https://github.com/torvalds/linux

FROM linux-amd64-dev-common AS linux-amd64-dev
RUN apt-get install -y libelf-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/bochs/linux_x86_config ./.config
RUN make ARCH=x86 CROSS_COMPILE=x86_64-linux-gnu- -j$(nproc) all && \
    mkdir /out && \
    mv /work-buildlinux/linux/arch/x86/boot/bzImage /out/bzImage && \
    make clean

FROM linux-amd64-dev-common AS linux-amd64-config-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/bochs/linux_x86_config ./.config
RUN make ARCH=x86 CROSS_COMPILE=x86_64-linux-gnu- olddefconfig

FROM scratch AS linux-amd64-config
COPY --link --from=linux-amd64-config-dev /work-buildlinux/linux/.config /

FROM gcc-x86-64-linux-gnu-base AS busybox-amd64-dev
ARG BUSYBOX_VERSION
RUN apt-get update -y && apt-get install -y gcc bzip2 wget
WORKDIR /work
RUN wget https://busybox.net/downloads/busybox-${BUSYBOX_VERSION}.tar.bz2
RUN bzip2 -d busybox-${BUSYBOX_VERSION}.tar.bz2
RUN tar xvf busybox-${BUSYBOX_VERSION}.tar
WORKDIR /work/busybox-${BUSYBOX_VERSION}
RUN make CROSS_COMPILE=x86_64-linux-gnu- LDFLAGS=--static defconfig
RUN make CROSS_COMPILE=x86_64-linux-gnu- LDFLAGS=--static -j$(nproc)
RUN mkdir -p /out/bin && mv busybox /out/bin/busybox
RUN make LDFLAGS=--static defconfig
RUN make LDFLAGS=--static -j$(nproc)
RUN for i in $(./busybox --list) ; do ln -s busybox /out/bin/$i ; done
RUN mkdir -p /out/usr/share/udhcpc/ && cp ./examples/udhcp/simple.script /out/usr/share/udhcpc/default.script

FROM golang-base AS runc-amd64-dev
ARG RUNC_VERSION
RUN apt-get update -y && apt-get install -y git make gperf
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc && \
    cd /go/src/github.com/opencontainers/runc && \
    git checkout "${RUNC_VERSION}" && \
    make static GOARCH=amd64 CC=gcc EXTRA_LDFLAGS='-s -w' BUILDTAGS="" EXTRA_LDFLAGS='-s -w' BUILDTAGS="" && \
    mkdir -p /out/ && mv runc /out/runc

FROM gcc-x86-64-linux-gnu-base AS tini-amd64-dev
# https://github.com/krallin/tini#building-tini
RUN apt-get update -y && apt-get install -y cmake
ENV CFLAGS="-DPR_SET_CHILD_SUBREAPER=36 -DPR_GET_CHILD_SUBREAPER=37"
WORKDIR /work
RUN git clone -b v0.19.0 https://github.com/krallin/tini
WORKDIR /work/tini
ENV CC="x86_64-linux-gnu-gcc -static"
RUN cmake . && make && mkdir /out/ && mv tini /out/

FROM gcc-x86-64-linux-gnu-base AS grub-amd64-dev
ARG LINUX_LOGLEVEL
RUN apt-get update && apt-get install -y mkisofs xorriso wget bison flex python-is-python3 gettext
WORKDIR /work/
RUN wget https://ftp.gnu.org/gnu/grub/grub-2.06.tar.gz
RUN tar zxvf grub-2.06.tar.gz
WORKDIR /work/grub-2.06
RUN ./configure --target=i386
RUN make -j$(nproc)
RUN make install
RUN mkdir -p /iso/boot/grub
COPY --link --from=linux-amd64-dev /out/bzImage /iso/boot/grub/
COPY --link --from=assets ./config/bochs/grub.cfg.template /
RUN cat /grub.cfg.template | LOGLEVEL=$LINUX_LOGLEVEL envsubst > /iso/boot/grub/grub.cfg
RUN mkdir /out && grub-mkrescue --directory ./grub-core -o /out/boot.iso /iso

FROM ubuntu AS bios-amd64-dev
RUN apt-get update && apt-get install -y build-essential git
COPY --link --from=bochs-repo / /Bochs
WORKDIR /Bochs/bochs
RUN CC="x86_64-linux-gnu-gcc" ./configure --enable-x86-64 --with-nogui
RUN make -j$(nproc) bios/BIOS-bochs-latest bios/VGABIOS-lgpl-latest
RUN mkdir /out/ && mv bios/BIOS-bochs-latest bios/VGABIOS-lgpl-latest /out/

FROM golang-base AS init-amd64-dev
COPY --link --from=assets / /work
WORKDIR /work
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOARCH=amd64 go build -ldflags "-s -w -extldflags '-static'" -tags "osusergo netgo static_build" -o /out/init ./cmd/init

FROM gcc-x86-64-linux-gnu-base AS vmtouch-amd64-dev
RUN git clone https://github.com/hoytech/vmtouch.git && \
    cd vmtouch && \
    CC="x86_64-linux-gnu-gcc -static" make && \
    mkdir /out && mv vmtouch /out/

FROM ubuntu:22.04 AS rootfs-amd64-dev
RUN apt-get update -y && apt-get install -y mkisofs
COPY --link --from=busybox-amd64-dev /out/ /rootfs/
COPY --link --from=runc-amd64-dev /out/runc /rootfs/sbin/runc
COPY --link --from=bundle-dev /out/ /rootfs/
COPY --link --from=init-amd64-dev /out/init /rootfs/sbin/init
COPY --link --from=vmtouch-amd64-dev /out/vmtouch /rootfs/bin/
COPY --link --from=tini-amd64-dev /out/tini /rootfs/sbin/tini
RUN mkdir -p /rootfs/proc /rootfs/sys /rootfs/mnt /rootfs/run /rootfs/tmp /rootfs/dev /rootfs/var /rootfs/etc && mknod /rootfs/dev/null c 1 3 && chmod 666 /rootfs/dev/null
RUN mkdir /out/ && mkisofs -R -o /out/rootfs.bin /rootfs/
# RUN isoinfo -i /out/rootfs.bin -l

FROM ubuntu:22.04 AS bochs-config-dev
ARG VM_MEMORY_SIZE_MB
RUN apt-get update && apt-get install -y gettext-base && mkdir /out
COPY --link --from=assets ./config/bochs/bochsrc.template /
RUN cat /bochsrc.template | MEMORY_SIZE=$VM_MEMORY_SIZE_MB envsubst > /out/bochsrc

FROM scratch AS vm-amd64-dev
COPY --link --from=grub-amd64-dev /out/boot.iso /pack/
COPY --link --from=bios-amd64-dev /out/ /pack/
COPY --link --from=rootfs-amd64-dev /out/rootfs.bin /pack/
COPY --link --from=bochs-config-dev /out/bochsrc /pack/

FROM ubuntu:22.04 AS gcc-aarch64-linux-gnu-base
RUN apt-get update && apt-get install -y gcc-aarch64-linux-gnu linux-libc-dev-arm64-cross git make

FROM gcc-aarch64-linux-gnu-base AS linux-aarch64-dev-common
RUN apt-get update && apt-get install -y gperf flex bison bc
RUN mkdir /work-buildlinux
WORKDIR /work-buildlinux
RUN git clone -b v6.1 --depth 1 https://github.com/torvalds/linux

FROM linux-aarch64-dev-common AS linux-aarch64-dev-qemu
RUN apt-get install -y libelf-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/qemu/linux_arm64_config ./.config
RUN make ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu- -j$(nproc) all && \
    mkdir /out && \
    mv /work-buildlinux/linux/arch/arm64/boot/Image /out/bzImage && \
    make clean

FROM linux-aarch64-dev-common AS linux-aarch64-config-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/qemu/linux_arm64_config ./.config
RUN make ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu- olddefconfig

FROM scratch AS linux-aarch64-config
COPY --link --from=linux-aarch64-config-dev /work-buildlinux/linux/.config /

FROM gcc-aarch64-linux-gnu-base AS busybox-aarch64-dev
ARG BUSYBOX_VERSION
RUN apt-get update -y && apt-get install -y gcc bzip2 wget
WORKDIR /work
RUN wget https://busybox.net/downloads/busybox-${BUSYBOX_VERSION}.tar.bz2
RUN bzip2 -d busybox-${BUSYBOX_VERSION}.tar.bz2
RUN tar xvf busybox-${BUSYBOX_VERSION}.tar
WORKDIR /work/busybox-${BUSYBOX_VERSION}
RUN make CROSS_COMPILE=aarch64-linux-gnu- LDFLAGS=--static defconfig
RUN make CROSS_COMPILE=aarch64-linux-gnu- LDFLAGS=--static -j$(nproc)
RUN mkdir -p /out/bin && mv busybox /out/bin/busybox
RUN make LDFLAGS=--static defconfig
RUN make LDFLAGS=--static -j$(nproc)
RUN for i in $(./busybox --list) ; do ln -s busybox /out/bin/$i ; done
RUN mkdir -p /out/usr/share/udhcpc/ && cp ./examples/udhcp/simple.script /out/usr/share/udhcpc/default.script

FROM golang-base AS runc-aarch64-dev
ARG RUNC_VERSION
RUN apt-get update -y && apt-get install -y git make gperf
RUN apt-get update -y && apt-get install -y gcc-aarch64-linux-gnu libc-dev-arm64-cross
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc && \
    cd /go/src/github.com/opencontainers/runc && \
    git checkout "${RUNC_VERSION}" && \
    make static GOARCH=arm64 CC=aarch64-linux-gnu-gcc EXTRA_LDFLAGS='-s -w' BUILDTAGS="" EXTRA_LDFLAGS='-s -w' BUILDTAGS="" && \
    mkdir -p /out/ && mv runc /out/runc

FROM gcc-aarch64-linux-gnu-base AS vmtouch-aarch64-dev
RUN git clone https://github.com/hoytech/vmtouch.git && \
    cd vmtouch && \
    CC="aarch64-linux-gnu-gcc -static" make && \
    mkdir /out && mv vmtouch /out/

FROM gcc-aarch64-linux-gnu-base AS tini-aarch64-dev
# https://github.com/krallin/tini#building-tini
RUN apt-get update -y && apt-get install -y cmake
ENV CFLAGS="-DPR_SET_CHILD_SUBREAPER=36 -DPR_GET_CHILD_SUBREAPER=37"
WORKDIR /work
RUN git clone -b v0.19.0 https://github.com/krallin/tini
WORKDIR /work/tini
ENV CC="aarch64-linux-gnu-gcc -static"
RUN cmake . && make && mkdir /out/ && mv tini /out/

FROM golang-base AS init-aarch64-dev
COPY --link --from=assets / /work
WORKDIR /work
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOARCH=arm64 go build -ldflags "-s -w -extldflags '-static'" -tags "osusergo netgo static_build" -o /out/init ./cmd/init

FROM ubuntu:22.04 AS rootfs-aarch64-dev
RUN apt-get update -y && apt-get install -y mkisofs
COPY --link --from=busybox-aarch64-dev /out/ /rootfs/
COPY --link --from=runc-aarch64-dev /out/runc /rootfs/sbin/runc
COPY --link --from=bundle-dev /out/ /rootfs/
COPY --link --from=init-aarch64-dev /out/init /rootfs/sbin/init
COPY --link --from=vmtouch-aarch64-dev /out/vmtouch /rootfs/bin/
COPY --link --from=tini-aarch64-dev /out/tini /rootfs/sbin/tini
RUN mkdir -p /rootfs/proc /rootfs/sys /rootfs/mnt /rootfs/run /rootfs/tmp /rootfs/dev /rootfs/var /rootfs/etc && mknod /rootfs/dev/null c 1 3 && chmod 666 /rootfs/dev/null
RUN mkdir /out/ && mkisofs -R -o /out/rootfs.bin /rootfs/

FROM linux-amd64-dev-common AS linux-amd64-dev-qemu
RUN apt-get install -y libelf-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/qemu/linux_x86_config ./.config
RUN make ARCH=x86 CROSS_COMPILE=x86_64-linux-gnu- -j$(nproc) all && \
    mkdir /out && \
    mv /work-buildlinux/linux/arch/x86/boot/bzImage /out/bzImage && \
    make clean

FROM linux-amd64-dev-common AS linux-amd64-config-dev-qemu
WORKDIR /work-buildlinux/linux
COPY --link --from=assets /config/qemu/linux_x86_config ./.config
RUN make ARCH=x86 CROSS_COMPILE=x86_64-linux-gnu- olddefconfig

FROM scratch AS linux-amd64-config-qemu
COPY --link --from=linux-amd64-config-dev-qemu /work-buildlinux/linux/.config /

FROM glib-emscripten-base AS qemu-emscripten-dev
COPY --link --from=qemu-repo / /qemu
WORKDIR /qemu
COPY --link --from=zlib-emscripten-dev /glib-emscripten/ /glib-emscripten/
COPY --link --from=glib-emscripten-dev /glib-emscripten/ /glib-emscripten/
COPY --link --from=pixman-emscripten-dev /glib-emscripten/ /glib-emscripten/
RUN mkdir -p build
WORKDIR /qemu/build
RUN npm i xterm-pty

FROM linux-riscv64-dev-common AS linux-riscv64-config-dev-qemu
WORKDIR /work-buildlinux/linux
COPY --link --from=assets /config/qemu/linux_rv64_config ./.config
RUN make ARCH=riscv CROSS_COMPILE=riscv64-linux-gnu- olddefconfig

FROM scratch AS linux-riscv64-config-qemu
COPY --link --from=linux-riscv64-config-dev-qemu /work-buildlinux/linux/.config /

FROM linux-riscv64-dev-common AS linux-riscv64-dev-qemu
RUN apt-get install -y libelf-dev
WORKDIR /work-buildlinux/linux
COPY --link --from=assets ./config/qemu/linux_rv64_config ./.config
RUN make ARCH=riscv CROSS_COMPILE=riscv64-linux-gnu- -j$(nproc) all && \
    mkdir /out && \
    mv /work-buildlinux/linux/arch/riscv/boot/Image /out/Image && \
    make clean

FROM golang-base AS get-qemu-state-dev
COPY --link --from=assets / /work
RUN mkdir /out/
WORKDIR /work
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -o /out/get-qemu-state ./cmd/get-qemu-state

FROM ubuntu:22.04 AS qemu-config-dev-amd64
ARG LINUX_LOGLEVEL
ARG VM_MEMORY_SIZE_MB
ARG VM_CORE_NUMS
ARG QEMU_MIGRATION
RUN apt-get update && apt-get install -y gettext-base && mkdir /out
COPY --link --from=assets /config/qemu/args-x86_64.json.template /args.json.template
RUN MIGRATION_FLAGS= ; \
    if test "${QEMU_MIGRATION}" = "true"  ; then \
      MIGRATION_FLAGS='"-incoming", "file:/pack/vm.state",' ; \
    fi && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION="" WASI0_PATH=/tmp/wasi0 WASI1_PATH=/tmp/wasi1 envsubst > /out/args-before-cp.json && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION=$MIGRATION_FLAGS WASI0_PATH=/ WASI1_PATH=/pack envsubst > /out/args.json
RUN echo "Module['arguments'] =" > /out/arg-module.js
RUN cat /out/args.json >> /out/arg-module.js
RUN echo ";" >> /out/arg-module.js

FROM ubuntu:22.04 AS qemu-config-dev-aarch64
ARG LINUX_LOGLEVEL
ARG VM_MEMORY_SIZE_MB
ARG VM_CORE_NUMS
ARG QEMU_MIGRATION
RUN apt-get update && apt-get install -y gettext-base && mkdir /out
COPY --link --from=assets /config/qemu/args-aarch64.json.template /args.json.template
RUN MIGRATION_FLAGS= ; \
    if test "${QEMU_MIGRATION}" = "true"  ; then \
      MIGRATION_FLAGS='"-incoming", "file:/pack/vm.state",' ; \
    fi && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION="" WASI0_PATH=/tmp/wasi0 WASI1_PATH=/tmp/wasi1 envsubst > /out/args-before-cp.json && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION=$MIGRATION_FLAGS WASI0_PATH=/ WASI1_PATH=/pack envsubst > /out/args.json
RUN echo "Module['arguments'] =" > /out/arg-module.js
RUN cat /out/args.json >> /out/arg-module.js
RUN echo ";" >> /out/arg-module.js

FROM ubuntu:22.04 AS qemu-config-dev-riscv64
ARG LINUX_LOGLEVEL
ARG VM_MEMORY_SIZE_MB
ARG VM_CORE_NUMS
ARG QEMU_MIGRATION
RUN apt-get update && apt-get install -y gettext-base && mkdir /out
COPY --link --from=assets /config/qemu/args-riscv64.json.template /args.json.template
RUN MIGRATION_FLAGS= ; \
    if test "${QEMU_MIGRATION}" = "true"  ; then \
      MIGRATION_FLAGS='"-incoming", "file:/pack/vm.state",' ; \
    fi && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION="" WASI0_PATH=/tmp/wasi0 WASI1_PATH=/tmp/wasi1 envsubst > /out/args-before-cp.json && \
    cat /args.json.template | LOGLEVEL=$LINUX_LOGLEVEL MEMORY_SIZE=$VM_MEMORY_SIZE_MB CORE_NUMS=$VM_CORE_NUMS MIGRATION=$MIGRATION_FLAGS WASI0_PATH=/ WASI1_PATH=/pack envsubst > /out/args.json
RUN echo "Module['arguments'] =" > /out/arg-module.js
RUN cat /out/args.json >> /out/arg-module.js
RUN echo ";" >> /out/arg-module.js

FROM gcc:14 AS qemu-native-dev
RUN apt-get update && apt-get install -y libffi-dev libglib2.0-dev libpixman-1-dev libattr1 libattr1-dev ninja-build pipx
RUN PIPX_BIN_DIR=/usr/local/bin pipx install meson==1.5.0
COPY --link --from=qemu-repo / /qemu

FROM qemu-native-dev AS qemu-x86_64-pack
WORKDIR /qemu/build/
RUN ../configure --static --target-list=x86_64-softmmu --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=ucontext --enable-virtfs --enable-attr
RUN make -j $(nproc) qemu-system-x86_64

RUN mkdir -p /pack/
COPY --link --from=rootfs-amd64-dev /out/rootfs.bin /pack/
COPY --link --from=linux-amd64-dev-qemu /out/bzImage /pack/
RUN cp /qemu/pc-bios/bios-256k.bin /pack/
RUN cp /qemu/pc-bios/kvmvapic.bin /pack/
RUN cp /qemu/pc-bios/linuxboot_dma.bin /pack/
RUN cp /qemu/pc-bios/vgabios-stdvga.bin /pack/
RUN cp /qemu/pc-bios/efi-virtio.rom /pack/

COPY --link --from=get-qemu-state-dev /out/get-qemu-state /get-qemu-state
COPY --link --from=qemu-config-dev-amd64 /out/args-before-cp.json /
RUN mkdir -p /tmp/wasi0 /tmp/wasi1
WORKDIR /qemu/build/
ARG QEMU_MIGRATION
RUN if test "${QEMU_MIGRATION}" = "true"  ; then /get-qemu-state -output=/pack/vm.state --args-json=/args-before-cp.json ./qemu-system-x86_64 ; fi

FROM qemu-native-dev AS qemu-aarch64-pack
WORKDIR /qemu/build/
RUN ../configure --static --target-list=aarch64-softmmu --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=ucontext --enable-virtfs --enable-attr
RUN make -j $(nproc) qemu-system-aarch64

RUN mkdir -p /pack/
COPY --link --from=rootfs-aarch64-dev /out/rootfs.bin /pack/
COPY --link --from=linux-aarch64-dev-qemu /out/bzImage /pack/
RUN cp /qemu/pc-bios/edk2-aarch64-code.fd.bz2 /pack/
RUN bzip2 -d /pack/edk2-aarch64-code.fd.bz2
RUN cp /qemu/pc-bios/efi-virtio.rom /pack/

COPY --link --from=get-qemu-state-dev /out/get-qemu-state /get-qemu-state
COPY --link --from=qemu-config-dev-aarch64 /out/args-before-cp.json /
RUN mkdir -p /tmp/wasi0 /tmp/wasi1
WORKDIR /qemu/build/
ARG QEMU_MIGRATION
RUN if test "${QEMU_MIGRATION}" = "true"  ; then /get-qemu-state -output=/pack/vm.state --args-json=/args-before-cp.json ./qemu-system-aarch64 ; fi

FROM qemu-native-dev AS qemu-riscv64-pack
WORKDIR /qemu/build/
RUN ../configure --static --target-list=riscv64-softmmu --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=ucontext --enable-virtfs --enable-attr
RUN make -j $(nproc) qemu-system-riscv64

RUN mkdir -p /pack/
COPY --link --from=rootfs-riscv64-dev /out/rootfs.bin /pack/
COPY --link --from=linux-riscv64-dev-qemu /out/Image /pack/
RUN cp /qemu/pc-bios/opensbi-riscv64-generic-fw_dynamic.bin /pack/
RUN cp /qemu/pc-bios/efi-virtio.rom /pack/

COPY --link --from=get-qemu-state-dev /out/get-qemu-state /get-qemu-state
COPY --link --from=qemu-config-dev-riscv64 /out/args-before-cp.json /
RUN mkdir -p /tmp/wasi0 /tmp/wasi1
WORKDIR /qemu/build/
ARG QEMU_MIGRATION
RUN if test "${QEMU_MIGRATION}" = "true"  ; then /get-qemu-state -output=/pack/vm.state --args-json=/args-before-cp.json ./qemu-system-riscv64 ; fi

FROM qemu-emscripten-dev AS qemu-emscripten-dev-amd64
ARG LOAD_MODE
RUN EXTRA_CFLAGS="-O3 -g -Wno-error=unused-command-line-argument -matomics -mbulk-memory -DNDEBUG -DG_DISABLE_ASSERT -D_GNU_SOURCE -sASYNCIFY=1 -pthread -sPROXY_TO_PTHREAD=1 -sFORCE_FILESYSTEM -sALLOW_TABLE_GROWTH -sTOTAL_MEMORY=$((3000*1024*1024)) -sWASM_BIGINT -sMALLOC=emmalloc --js-library=/qemu/build/node_modules/xterm-pty/emscripten-pty.js -sEXPORT_ES6=1 -sASYNCIFY_IMPORTS=ffi_call_js" ; \
    emconfigure ../configure --static --target-list=x86_64-softmmu --cpu=wasm32 --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=fiber --enable-virtfs \
    --extra-cflags="$EXTRA_CFLAGS" --extra-cxxflags="$EXTRA_CFLAGS" --extra-ldflags="-sEXPORTED_RUNTIME_METHODS=getTempRet0,setTempRet0,addFunction,removeFunction,TTY,FS" && \
    emmake make -j $(nproc) qemu-system-x86_64
COPY --from=qemu-x86_64-pack /pack /pack
RUN if test "${LOAD_MODE}" = "single" ; then \
      /emsdk/upstream/emscripten/tools/file_packager.py qemu-system-x86_64.data --preload /pack > load.js ; \
    else \
      mkdir /load && \
      mkdir /image && cp /pack/bzImage /image/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/image.data --preload /image > /load/image-load.js && \
      mkdir /rootfs && cp /pack/rootfs.bin /rootfs/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/rootfs.data --preload /rootfs > /load/rootfs-load.js && \
      mkdir /bios && \
      cp /pack/bios-256k.bin /bios/ && \
      cp /pack/kvmvapic.bin /bios/ && \
      cp /pack/linuxboot_dma.bin /bios/ && \
      cp /pack/vgabios-stdvga.bin /bios/ && \
      /emsdk/upstream/emscripten/tools/file_packager.py /load/bios.data --preload /bios > /load/bios-load.js ; \
    fi

FROM scratch AS js-qemu-amd64-base
COPY --link --from=qemu-emscripten-dev-amd64 /qemu/build/qemu-system-x86_64 /out.js
COPY --link --from=qemu-emscripten-dev-amd64 /qemu/build/qemu-system-x86_64.wasm /
COPY --link --from=qemu-emscripten-dev-amd64 /qemu/build/qemu-system-x86_64.worker.js /
COPY --link --from=qemu-config-dev-amd64 /out/arg-module.js /

FROM js-qemu-amd64-base AS js-qemu-amd64-single
COPY --link --from=qemu-emscripten-dev-amd64 /qemu/build/qemu-system-x86_64.data /
COPY --link --from=qemu-emscripten-dev-amd64 /qemu/build/load.js /

FROM js-qemu-amd64-base AS js-qemu-amd64-separated
COPY --link --from=qemu-emscripten-dev-amd64 /load /

FROM js-qemu-amd64-${LOAD_MODE} AS js-qemu-amd64

FROM qemu-emscripten-dev AS qemu-emscripten-dev-aarch64
ARG LOAD_MODE
RUN EXTRA_CFLAGS="-O3 -g -Wno-error=unused-command-line-argument -matomics -mbulk-memory -DNDEBUG -DG_DISABLE_ASSERT -D_GNU_SOURCE -sASYNCIFY=1 -pthread -sPROXY_TO_PTHREAD=1 -sFORCE_FILESYSTEM -sALLOW_TABLE_GROWTH -sTOTAL_MEMORY=2300MB -sWASM_BIGINT -sMALLOC=emmalloc --js-library=/qemu/build/node_modules/xterm-pty/emscripten-pty.js -sEXPORT_ES6=1 " ; \
    emconfigure ../configure --static --target-list=aarch64-softmmu --cpu=wasm32 --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=fiber --enable-virtfs \
    --extra-cflags="$EXTRA_CFLAGS" --extra-cxxflags="$EXTRA_CFLAGS" --extra-ldflags="-sEXPORTED_RUNTIME_METHODS=getTempRet0,setTempRet0,addFunction,removeFunction,TTY,FS" && \
    emmake make -j $(nproc) qemu-system-aarch64
COPY --from=qemu-aarch64-pack /pack /pack
RUN if test "${LOAD_MODE}" = "single" ; then \
      /emsdk/upstream/emscripten/tools/file_packager.py qemu-system-aarch64.data --preload /pack > load.js ; \
    else \
      mkdir /load && \
      mkdir /image && cp /pack/bzImage /image/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/image.data --preload /image > /load/image-load.js && \
      mkdir /rootfs && cp /pack/rootfs.bin /rootfs/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/rootfs.data --preload /rootfs > /load/rootfs-load.js && \
      mkdir /edk2 && cp /pack/edk2-aarch64-code.fd /edk2/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/edk2.data --preload /edk2 > /load/edk2-load.js ; \
    fi

FROM scratch AS js-qemu-aarch64-base
COPY --link --from=qemu-emscripten-dev-aarch64 /qemu/build/qemu-system-aarch64 /out.js
COPY --link --from=qemu-emscripten-dev-aarch64 /qemu/build/qemu-system-aarch64.wasm /
COPY --link --from=qemu-emscripten-dev-aarch64 /qemu/build/qemu-system-aarch64.worker.js /
COPY --link --from=qemu-config-dev-aarch64 /out/arg-module.js /

FROM js-qemu-aarch64-base AS js-qemu-aarch64-single
COPY --link --from=qemu-emscripten-dev-aarch64 /qemu/build/qemu-system-aarch64.data /
COPY --link --from=qemu-emscripten-dev-aarch64 /qemu/build/load.js /

FROM js-qemu-aarch64-base AS js-qemu-aarch64-separated
COPY --link --from=qemu-emscripten-dev-aarch64 /load /

FROM js-qemu-aarch64-$LOAD_MODE AS js-aarch64

FROM qemu-emscripten-dev AS qemu-emscripten-dev-riscv64
ARG LOAD_MODE
RUN EXTRA_CFLAGS="-O3 -g -Wno-error=unused-command-line-argument -matomics -mbulk-memory -DNDEBUG -DG_DISABLE_ASSERT -D_GNU_SOURCE -sASYNCIFY=1 -pthread -sPROXY_TO_PTHREAD=1 -sFORCE_FILESYSTEM -sALLOW_TABLE_GROWTH -sTOTAL_MEMORY=2300MB -sWASM_BIGINT -sMALLOC=emmalloc --js-library=/qemu/build/node_modules/xterm-pty/emscripten-pty.js -sEXPORT_ES6=1 -sASYNCIFY_IMPORTS=ffi_call_js" ; \
    emconfigure ../configure --static --target-list=riscv64-softmmu --cpu=wasm32 --cross-prefix= \
    --without-default-features --enable-system --with-coroutine=fiber --enable-virtfs \
    --extra-cflags="$EXTRA_CFLAGS" --extra-cxxflags="$EXTRA_CFLAGS" --extra-ldflags="-sEXPORTED_RUNTIME_METHODS=getTempRet0,setTempRet0,addFunction,removeFunction,TTY,FS" && \
    emmake make -j $(nproc) qemu-system-riscv64
COPY --from=qemu-riscv64-pack /pack /pack
RUN if test "${LOAD_MODE}" = "single" ; then \
      /emsdk/upstream/emscripten/tools/file_packager.py qemu-system-riscv64.data --preload /pack > load.js ; \
    else \
      mkdir /load && \
      mkdir /image && cp /pack/Image /image/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/image.data --preload /image > /load/image-load.js && \
      mkdir /rootfs && cp /pack/rootfs.bin /rootfs/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/rootfs.data --preload /rootfs > /load/rootfs-load.js && \
      mkdir /bios && cp /pack/opensbi-riscv64-generic-fw_dynamic.bin /bios/ && /emsdk/upstream/emscripten/tools/file_packager.py /load/bios.data --preload /bios > /load/bios-load.js ; \
    fi

FROM scratch AS js-qemu-riscv64-base
COPY --link --from=qemu-emscripten-dev-riscv64 /qemu/build/qemu-system-riscv64 /out.js
COPY --link --from=qemu-emscripten-dev-riscv64 /qemu/build/qemu-system-riscv64.wasm /
COPY --link --from=qemu-emscripten-dev-riscv64 /qemu/build/qemu-system-riscv64.worker.js /
COPY --link --from=qemu-config-dev-riscv64 /out/arg-module.js /

FROM js-qemu-riscv64-base AS js-qemu-riscv64-single
COPY --link --from=qemu-emscripten-dev-riscv64 /qemu/build/qemu-system-riscv64.data /
COPY --link --from=qemu-emscripten-dev-riscv64 /qemu/build/load.js /

FROM js-qemu-riscv64-base AS js-qemu-riscv64-separated
COPY --link --from=qemu-emscripten-dev-riscv64 /load /

FROM js-qemu-riscv64-${LOAD_MODE} AS js-qemu-riscv64

FROM js-qemu-riscv64 AS js-riscv64

FROM rust:1.74.1-bullseye AS bochs-dev-common
ARG WASI_VFS_VERSION
ARG WASI_SDK_VERSION
ARG WASI_SDK_VERSION_FULL
ARG BINARYEN_VERSION
ARG WIZER_VERSION
RUN apt-get update -y && apt-get install -y make curl git gcc xz-utils

WORKDIR /wasi
RUN curl -o wasi-sdk.tar.gz -fSL https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION}/wasi-sdk-${WASI_SDK_VERSION_FULL}-linux.tar.gz && \
    tar xvf wasi-sdk.tar.gz && rm wasi-sdk.tar.gz
ENV WASI_SDK_PATH=/wasi/wasi-sdk-${WASI_SDK_VERSION_FULL}

WORKDIR /work/
RUN git clone https://github.com/kateinoigakukun/wasi-vfs.git --recurse-submodules && \
    cd wasi-vfs && \
    git checkout "${WASI_VFS_VERSION}" && \
    cargo build --target wasm32-unknown-unknown && \
    cargo build --package wasi-vfs-cli && \
    mkdir -p /tools/wasi-vfs/ && \
    mv target/debug/wasi-vfs target/wasm32-unknown-unknown/debug/libwasi_vfs.a /tools/wasi-vfs/ && \
    cargo clean

WORKDIR /work/
RUN git clone https://github.com/bytecodealliance/wizer && \
    cd wizer && \
    git checkout "${WIZER_VERSION}" && \
    cargo build --bin wizer --all-features && \
    mkdir -p /tools/wizer/ && \
    mv include target/debug/wizer /tools/wizer/ && \
    cargo clean

RUN wget -O /tmp/binaryen.tar.gz https://github.com/WebAssembly/binaryen/releases/download/version_${BINARYEN_VERSION}/binaryen-version_${BINARYEN_VERSION}-x86_64-linux.tar.gz
RUN mkdir -p /binaryen
RUN tar -C /binaryen -zxvf /tmp/binaryen.tar.gz

COPY --link --from=bochs-repo / /Bochs

WORKDIR /Bochs/bochs/wasi_extra/jmp
RUN mkdir /jmp && cp jmp.h /jmp/
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-unknown-wasi -c jmp.c -I . -o jmp.o
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-unknown-wasi -Wl,--export=wasm_setjmp -c jmp.S -o jmp_wrapper.o
RUN ${WASI_SDK_PATH}/bin/wasm-ld jmp.o jmp_wrapper.o --export=wasm_setjmp --export=wasm_longjmp --export=handle_jmp --no-entry -r -o /jmp/jmp

WORKDIR /Bochs/bochs/wasi_extra/vfs
RUN mkdir /vfs
RUN ${WASI_SDK_PATH}/bin/clang --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -O2 --target=wasm32-unknown-wasi -c vfs.c -I . -o /vfs/vfs.o

WORKDIR /Bochs/bochs
ARG INIT_DEBUG
RUN LOGGING_FLAG=--disable-logging && \
    if test "${INIT_DEBUG}" = "true" ; then LOGGING_FLAG=--enable-logging ; fi && \
    CC="${WASI_SDK_PATH}/bin/clang" CXX="${WASI_SDK_PATH}/bin/clang++" RANLIB="${WASI_SDK_PATH}/bin/ranlib" \
    CFLAGS="--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -D_WASI_EMULATED_SIGNAL -DWASI -D__GNU__ -O2 -I/jmp/ -I/tools/wizer/include/" \
    CXXFLAGS="${CFLAGS}" \
    ./configure --host wasm32-unknown-wasi --enable-x86-64 --with-nogui --enable-usb --enable-usb-ehci \
    --disable-large-ramfile --disable-show-ips --disable-stats ${LOGGING_FLAG} \
    --enable-repeat-speedups --enable-fast-function-calls --disable-trace-linking --enable-handlers-chaining --enable-avx # TODO: --enable-trace-linking causes "out of bounds memory access"
RUN make -j$(nproc) bochs EMU_DEPS="/tools/wasi-vfs/libwasi_vfs.a /jmp/jmp /vfs/vfs.o -lrt"
RUN /binaryen/binaryen-version_${BINARYEN_VERSION}/bin/wasm-opt bochs --asyncify -O2 -o bochs.async --pass-arg=asyncify-ignore-imports
RUN mv bochs.async bochs

FROM bochs-dev-common AS bochs-dev-native
COPY --link --from=vm-amd64-dev /pack /minpack

FROM bochs-dev-common AS bochs-dev-wizer
COPY --link --from=vm-amd64-dev /pack /pack
ENV WASMTIME_BACKTRACE_DETAILS=1
RUN mv bochs bochs-org && /tools/wizer/wizer --allow-wasi --wasm-bulk-memory=true -r _start=wizer.resume --mapdir /pack::/pack -o bochs bochs-org
RUN mkdir /minpack && cp /pack/rootfs.bin /minpack/ && cp /pack/boot.iso /minpack/

FROM bochs-dev-${OPTIMIZATION_MODE} AS bochs-dev-packed
RUN /tools/wasi-vfs/wasi-vfs pack /Bochs/bochs/bochs --mapdir /pack::/minpack -o packed && mkdir /out
ARG OUTPUT_NAME
RUN mv packed /out/$OUTPUT_NAME

FROM scratch AS wasi-amd64
COPY --link --from=bochs-dev-packed /out/ /


FROM emscripten/emsdk:$EMSDK_VERSION AS bochs-emscripten
RUN apt-get install -y wget git
COPY --link --from=bochs-repo / /Bochs
WORKDIR /Bochs/bochs
COPY --link --from=vm-amd64-dev /pack /pack
ARG INIT_DEBUG
RUN LOGGING_FLAG=--disable-logging && \
    if test "${INIT_DEBUG}" = "true" ; then LOGGING_FLAG=--enable-logging ; fi && \
    CFLAGS="-O2 -s WASM=1 -s ASYNCIFY=1 -s ALLOW_MEMORY_GROWTH=1  -s TOTAL_MEMORY=$((30*1024*1024)) -sNO_EXIT_RUNTIME=1 -sFORCE_FILESYSTEM=1 -D__GNU__" \
    CXXFLAGS="${CFLAGS}" \
    emconfigure ./configure --host wasm32-unknown-emscripten --enable-x86-64 --with-nogui --enable-usb --enable-usb-ehci \
    --disable-large-ramfile --disable-show-ips --disable-stats ${LOGGING_FLAG} \
    --enable-repeat-speedups --enable-fast-function-calls --disable-trace-linking --enable-handlers-chaining --enable-avx # TODO: --enable-trace-linking causes "too much recursion"
RUN emmake make -j$(nproc) bochs EMU_DEPS="--preload-file /pack"
RUN mkdir -p /out/ && mv bochs /out/out.js && mv bochs.wasm /out/ && mv bochs.data /out/

FROM scratch AS js-bochs-amd64
COPY --link --from=bochs-emscripten /out/ /

FROM js-qemu-amd64 AS js-amd64

FROM js-$TARGETARCH AS js

FROM wasi-$TARGETARCH
