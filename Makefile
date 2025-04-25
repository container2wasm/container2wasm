CMD_DESTDIR ?= /usr/local
PREFIX ?= $(CURDIR)/out/

PKG=github.com/ktock/container2wasm
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
REVISION=$(shell git rev-parse HEAD)$(shell if ! git diff --no-ext-diff --quiet --exit-code; then echo .m; fi)
GO_EXTRA_LDFLAGS=-extldflags '-static'
GO_LD_FLAGS=-ldflags '-s -w -X $(PKG)/version.Version=$(VERSION) -X $(PKG)/version.Revision=$(REVISION) $(GO_EXTRA_LDFLAGS)'
GO_BUILDTAGS=-tags "osusergo netgo static_build"
GO_MODULE_DIRS=$(shell find . -type f -name go.mod -exec dirname {} \;)

all: c2w c2w-net

build: c2w c2w-net

c2w:
	CGO_ENABLED=0 go build -o $(PREFIX)/c2w $(GO_LD_FLAGS) $(GO_BUILDTAGS) -v ./cmd/c2w

c2w-net:
	CGO_ENABLED=0 go build -o $(PREFIX)/c2w-net $(GO_LD_FLAGS) $(GO_BUILDTAGS) -v ./cmd/c2w-net

c2w-net-proxy.wasm:
	cd extras/c2w-net-proxy/ ; GOOS=wasip1 GOARCH=wasm go build -o $(PREFIX)/c2w-net-proxy.wasm .

imagemounter.wasm:
	cd extras/imagemounter ; GOOS=wasip1 GOARCH=wasm go build -o $(PREFIX)/imagemounter.wasm .

install:
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		install -m 755 $(PREFIX)/c2w $(CMD_DESTDIR)/bin; \
		install -m 755 $(PREFIX)/c2w-net $(CMD_DESTDIR)/bin; \
	else \
		install -D -m 755 $(PREFIX)/c2w $(CMD_DESTDIR)/bin; \
		install -D -m 755 $(PREFIX)/c2w-net $(CMD_DESTDIR)/bin; \
	fi

artifacts: clean
	GOOS=linux GOARCH=amd64 make c2w c2w-net
	tar -C $(PREFIX) --owner=0 --group=0 -zcvf $(PREFIX)/container2wasm-$(VERSION)-linux-amd64.tar.gz c2w c2w-net

	GOOS=linux GOARCH=arm64 make c2w c2w-net
	tar -C $(PREFIX) --owner=0 --group=0 -zcvf $(PREFIX)/container2wasm-$(VERSION)-linux-arm64.tar.gz c2w c2w-net

	rm -f $(PREFIX)/c2w $(PREFIX)/c2w-net

test:
	./tests/test.sh

benchmark:
	./tests/bench.sh

vendor:
	$(foreach dir,$(GO_MODULE_DIRS),(cd $(dir) && go mod tidy) || exit 1;)

validate-vendor:
	$(eval TMPDIR := $(shell mktemp -d))
	cp -R $(CURDIR) ${TMPDIR}
	(cd ${TMPDIR}/container2wasm && make vendor)
	diff -r -u -q $(CURDIR) ${TMPDIR}/container2wasm
	rm -rf ${TMPDIR}

# Allowlist Licenses are listed in https://github.com/cncf/foundation/blob/a43993349f8c8d27e82e4c57399bf0f6e527a337/allowed-third-party-license-policy.md
ALLOWLIST_LICENSES=Apache-2.0,BSD-2-Clause,BSD-2-Clause-FreeBSD,BSD-3-Clause,MIT,ISC,OpenSSL,Python-2.0,PostgreSQL,UPL-1.0,X11,Zlib
# Our go submodules can be ignored because they are licensed under Apache-2.0. However go-licenses fails to detect the LICENSE file https://github.com/google/go-licenses/issues/186
# Dependencies from the ignored packages are still checked.
IGNORELIST := c2w-net-proxy,imagemounter,imagemounter-test,wazero,c2w-net-proxy-test
# License exception of github.com/hashicorp/errwrap (MPL-2.0) is listed in the following
# https://github.com/cncf/foundation/blob/a43993349f8c8d27e82e4c57399bf0f6e527a337/license-exceptions/CNCF-licensing-exceptions.csv#L423
IGNORELIST := $(IGNORELIST),github.com/hashicorp/errwrap
# License exception of github.com/hashicorp/go-cleanhttp (MPL-2.0) is listed in the following
# https://github.com/cncf/foundation/blob/a43993349f8c8d27e82e4c57399bf0f6e527a337/license-exceptions/CNCF-licensing-exceptions.csv#L424
IGNORELIST := $(IGNORELIST),github.com/hashicorp/go-cleanhttp
# License exception of github.com/hashicorp/go-multierror (MPL-2.0) is listed in the following
# https://github.com/cncf/foundation/blob/a43993349f8c8d27e82e4c57399bf0f6e527a337/license-exceptions/CNCF-licensing-exceptions.csv#L425
IGNORELIST := $(IGNORELIST),github.com/hashicorp/go-multierror
# License exception of github.com/hashicorp/go-retryablehttp (MPL-2.0) is listed in the following
# https://github.com/cncf/foundation/blob/a43993349f8c8d27e82e4c57399bf0f6e527a337/license-exceptions/CNCF-licensing-exceptions.csv#L430
IGNORELIST := $(IGNORELIST),github.com/hashicorp/go-retryablehttp

go-licenses:
	$(foreach dir,$(GO_MODULE_DIRS),(set -eux ; cd $(dir) ; go-licenses check --include_tests ./... --ignore=$(IGNORELIST) --allowed_licenses=$(ALLOWLIST_LICENSES)) || exit 1;)

clean:
	rm -f $(CURDIR)/out/*
