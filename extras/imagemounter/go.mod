module imagemounter

go 1.26.6

require (
	github.com/containerd/platforms v1.0.0-rc.5
	github.com/containerd/stargz-snapshotter v0.19.1
	github.com/containerd/stargz-snapshotter/estargz v0.19.1
	github.com/containers/gvisor-tap-vsock v0.9.0
	github.com/hugelgupf/p9 v0.0.0-00010101000000-000000000000
	github.com/moby/sys/user v0.4.1
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/opencontainers/runtime-spec v1.3.0
	github.com/sirupsen/logrus v1.10.2
	golang.org/x/sync v0.25.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/cgroups/v3 v3.1.3 // indirect
	github.com/containerd/containerd/api v1.12.0 // indirect
	github.com/containerd/continuity v0.5.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/log/otel v0.1.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inetaf/tcpproxy v0.0.0-20260515195445-c159a6051109 // indirect
	github.com/linuxkit/virtsock v0.0.0-20241009230534-cb6a20cc0422 // indirect
	github.com/mdlayher/socket v0.6.0 // indirect
	github.com/mdlayher/vsock v1.3.0 // indirect
	github.com/moby/sys/userns v0.2.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk v1.46.0 // indirect
	golang.org/x/exp v0.0.0-20241108190413-2d47ceb2692f // indirect
)

require (
	github.com/Microsoft/go-winio v0.6.3 // indirect
	github.com/Microsoft/hcsshim v0.15.0-rc.4 // indirect
	github.com/containerd/containerd/v2 v2.4.2
	github.com/containerd/log v0.2.0 // indirect
	github.com/containerd/ttrpc v1.2.9 // indirect
	github.com/containerd/typeurl/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/insomniacslk/dhcp v0.0.0-20240710054256-ddd8a41251c9 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/vbatts/tar-split v0.12.3 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gvisor.dev/gvisor v0.0.0-20260413194555-9680d69bf798 // indirect
)

// Patched for enabling to compile it to wasi
replace github.com/insomniacslk/dhcp => github.com/ktock/insomniacslk-dhcp v0.0.0-20261124083302-a5583a89b37b

// Patched for enabling to compile it to wasi
replace github.com/u-root/uio => github.com/ktock/u-root-uio v0.0.0-20261124083304-76c2dbb0df30

replace github.com/hugelgupf/p9 => github.com/ktock/p9 v0.0.0-20261124084322-19c04381b4f8

replace github.com/containerd/stargz-snapshotter => github.com/ktock/stargz-snapshotter v0.0.1-0.20261124084052-5efefb5b54b5

replace github.com/containerd/containerd/v2 => github.com/ktock/containerd/v2 v2.0.0-20261124084326-82a8755c350b

replace github.com/containerd/stargz-snapshotter/estargz => github.com/ktock/stargz-snapshotter/estargz v0.0.0-20261124084052-5efefb5b54b5

// FIXME: Temporary use a forked repostory which removed an unused package for reducing dependencies (see #454).
replace github.com/containers/gvisor-tap-vsock => github.com/ktock/gvisor-tap-vsock v0.0.0-20261124083637-2a28e50bfd52

replace github.com/moby/sys/user => github.com/moby/sys/user v0.3.0
