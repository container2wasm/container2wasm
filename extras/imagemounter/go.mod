module stack

go 1.22

require (
	github.com/containerd/containerd v1.7.21
	github.com/containerd/platforms v0.2.1
	github.com/containerd/stargz-snapshotter v0.15.1
	github.com/containerd/stargz-snapshotter/estargz v0.15.1
	github.com/containers/gvisor-tap-vsock v0.7.3
	github.com/hugelgupf/p9 v0.0.0-00010101000000-000000000000
	github.com/moby/sys/user v0.3.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/opencontainers/runtime-spec v1.2.1
	github.com/sirupsen/logrus v1.9.3
	golang.org/x/sync v0.11.0
)

require (
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230811130428-ced1acdcaa24 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/Microsoft/hcsshim v0.11.1 // indirect
	github.com/apparentlymart/go-cidr v1.1.0 // indirect
	github.com/containerd/cgroups v1.1.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/ttrpc v1.2.2 // indirect
	github.com/containerd/typeurl/v2 v2.1.1 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.4 // indirect
	github.com/insomniacslk/dhcp v0.0.0-20220504074936-1ca156eafb9f // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/miekg/dns v1.1.58 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/sys/mountinfo v0.7.1 // indirect
	github.com/opencontainers/runc v1.1.5 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/u-root/uio v0.0.0-20210528114334-82958018845c // indirect
	github.com/vbatts/tar-split v0.11.5 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel v1.14.0 // indirect
	go.opentelemetry.io/otel/trace v1.14.0 // indirect
	golang.org/x/crypto v0.22.0 // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/net v0.24.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.17.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230822172742-b8732ec3820d // indirect
	google.golang.org/grpc v1.59.0 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gvisor.dev/gvisor v0.0.0-20231023213702-2691a8f9b1cf // indirect
	inet.af/tcpproxy v0.0.0-20220326234310-be3ee21c9fa0 // indirect
)

replace github.com/sirupsen/logrus => github.com/sirupsen/logrus v1.9.3-0.20230531171720-7165f5e779a5

// Patched for enabling to compile it to wasi
replace github.com/insomniacslk/dhcp => github.com/ktock/insomniacslk-dhcp v0.0.0-20230911142651-b86573a014b1

// Patched for enabling to compile it to wasi
replace github.com/u-root/uio => github.com/ktock/u-root-uio v0.0.0-20230911142931-5cf720bc8a29

replace github.com/hugelgupf/p9 => github.com/ktock/p9 v0.0.0-20240109162600-dac0f97fe886

replace github.com/containerd/stargz-snapshotter => github.com/ktock/stargz-snapshotter v0.0.1-0.20240304123548-c3fcce188d68

replace github.com/containerd/containerd => github.com/ktock/containerd v1.2.1-0.20240109162750-28ec64e033db

replace github.com/containerd/stargz-snapshotter/estargz => github.com/ktock/stargz-snapshotter/estargz v0.0.0-20240304123548-c3fcce188d68
