module c2w-net-proxy

go 1.23.0

toolchain go1.23.2

require (
	github.com/containers/gvisor-tap-vsock v0.7.3
	github.com/sirupsen/logrus v1.9.3
)

require (
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/apparentlymart/go-cidr v1.1.0 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/insomniacslk/dhcp v0.0.0-20220504074936-1ca156eafb9f // indirect
	github.com/miekg/dns v1.1.58 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/u-root/uio v0.0.0-20210528114334-82958018845c // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.17.0 // indirect
	gvisor.dev/gvisor v0.0.0-20231023213702-2691a8f9b1cf // indirect
	inet.af/tcpproxy v0.0.0-20220326234310-be3ee21c9fa0 // indirect
)

replace github.com/sirupsen/logrus => github.com/sirupsen/logrus v1.9.3-0.20230531171720-7165f5e779a5

// Patched for enabling to compile it to wasi
replace github.com/insomniacslk/dhcp => github.com/ktock/insomniacslk-dhcp v0.0.0-20230911142651-b86573a014b1

// Patched for enabling to compile it to wasi
replace github.com/u-root/uio => github.com/ktock/u-root-uio v0.0.0-20230911142931-5cf720bc8a29
