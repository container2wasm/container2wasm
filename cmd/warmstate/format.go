package warmstate

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const Magic = "C2WW"

const MarkerVersion = 1

// Header describes a bash-ready guest memory sidecar for external-bundle VMs.
type Header struct {
	MarkerVersion  int    `json:"markerVersion"`
	SourceHash     string `json:"sourceHash"`
	OciDigest      string `json:"ociDigest"`
	VMMemorySizeMb int    `json:"vmMemorySizeMb"`
	MemoryBytes    int    `json:"memoryBytes"`
	GuestRamOffset int    `json:"guestRamOffset"`
	GuestRamSize   int    `json:"guestRamSize"`
}

func Write(path string, header Header, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("warm state payload is empty")
	}
	header.MemoryBytes = len(payload)
	if header.MarkerVersion == 0 {
		header.MarkerVersion = MarkerVersion
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 4)
	copy(buf, Magic)
	if _, err := f.Write(buf); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buf, uint32(header.MarkerVersion))
	if _, err := f.Write(buf); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buf, uint32(len(headerJSON)))
	if _, err := f.Write(buf); err != nil {
		return err
	}
	if _, err := f.Write(headerJSON); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	return nil
}

func Read(path string) (Header, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Header{}, nil, err
	}
	if len(data) < 12 {
		return Header{}, nil, errors.New("warm state file too short")
	}
	if string(data[:4]) != Magic {
		return Header{}, nil, fmt.Errorf("invalid warm state magic %q", string(data[:4]))
	}
	version := int(binary.LittleEndian.Uint32(data[4:8]))
	headerLen := int(binary.LittleEndian.Uint32(data[8:12]))
	if headerLen < 0 || 12+headerLen > len(data) {
		return Header{}, nil, errors.New("invalid warm state header length")
	}
	var header Header
	if err := json.Unmarshal(data[12:12+headerLen], &header); err != nil {
		return Header{}, nil, err
	}
	if header.MarkerVersion == 0 {
		header.MarkerVersion = version
	}
	payload := data[12+headerLen:]
	if header.MemoryBytes > 0 && header.MemoryBytes != len(payload) {
		return Header{}, nil, fmt.Errorf("payload size mismatch: header=%d actual=%d", header.MemoryBytes, len(payload))
	}
	return header, payload, nil
}
