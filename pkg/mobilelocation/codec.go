// Package mobilelocation implements bounded Apple network-location response
// processing. Protocol field numbers are informed by location-spoofer; this
// codec is independently implemented and does not embed its AGPL source.
package mobilelocation

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
)

const bodyLimit = 1 << 20

type Settings struct {
	Enabled   bool    `json:"enabled"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Accuracy  int     `json:"accuracy"`
}

func (s Settings) Validate() error {
	if math.IsNaN(s.Latitude) || math.IsInf(s.Latitude, 0) || s.Latitude < -90 || s.Latitude > 90 ||
		math.IsNaN(s.Longitude) || math.IsInf(s.Longitude, 0) || s.Longitude < -180 || s.Longitude > 180 {
		return errors.New("latitude must be -90 to 90; longitude must be -180 to 180")
	}
	if s.Accuracy < 1 || s.Accuracy > 1000 {
		return errors.New("accuracy must be 1 to 1000 meters")
	}
	return nil
}

type field struct {
	number        int
	kind          uint64
	data, encoded []byte
}

func decodeMessage(b []byte) ([]field, error) {
	fields := make([]field, 0, 8)
	for offset := 0; offset < len(b); {
		start := offset
		tag, n := binary.Uvarint(b[offset:])
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid message tag")
		}
		offset += n
		f := field{number: int(tag >> 3), kind: tag & 7}
		valueStart := offset
		switch f.kind {
		case 0:
			_, n = binary.Uvarint(b[offset:])
			if n <= 0 {
				return nil, errors.New("invalid integer")
			}
			offset += n
		case 1:
			offset += 8
		case 5:
			offset += 4
		case 2:
			length, count := binary.Uvarint(b[offset:])
			if count <= 0 {
				return nil, errors.New("invalid message length")
			}
			offset += count
			if length > uint64(len(b)-offset) {
				return nil, errors.New("message length exceeds body")
			}
			valueStart = offset
			offset += int(length)
		default:
			return nil, errors.New("unsupported message wire type")
		}
		if offset > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		f.data, f.encoded = b[valueStart:offset], b[start:offset]
		fields = append(fields, f)
		if len(fields) > 32768 {
			return nil, errors.New("too many message fields")
		}
	}
	return fields, nil
}

func encodeField(f field, data []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(f.number)<<3|f.kind)
	if f.kind == 2 {
		b = binary.AppendUvarint(b, uint64(len(data)))
	}
	return append(b, data...)
}

var macAddress = regexp.MustCompile(`(?i)^[0-9a-f]{1,2}(:[0-9a-f]{1,2}){5}$`)

func replaceCoordinates(b []byte, s Settings) ([]byte, int, error) {
	fs, err := decodeMessage(b)
	if err != nil {
		return nil, 0, err
	}
	latitude, longitude, accuracy := false, false, false
	for _, f := range fs {
		latitude = latitude || f.number == 1 && f.kind == 0
		longitude = longitude || f.number == 2 && f.kind == 0
		accuracy = accuracy || f.number == 3 && f.kind == 0
	}
	if !latitude || !longitude {
		return b, 0, nil
	}
	result := make([]byte, 0, len(b)+16)
	for _, f := range fs {
		if f.kind == 0 && f.number >= 1 && f.number <= 3 {
			var value uint64
			switch f.number {
			case 1:
				value = uint64(int64(math.Round(s.Latitude * 1e8)))
			case 2:
				value = uint64(int64(math.Round(s.Longitude * 1e8)))
			case 3:
				value = uint64(s.Accuracy)
			}
			result = append(result, encodeField(f, binary.AppendUvarint(nil, value))...)
		} else {
			result = append(result, f.encoded...)
		}
	}
	if !accuracy {
		result = append(result, encodeField(field{number: 3, kind: 0}, binary.AppendUvarint(nil, uint64(s.Accuracy)))...)
	}
	return result, 1, nil
}

// Only recognized Wi-Fi access point and cellular records contain writable
// coordinates. Preserve unknown fields and records byte-for-byte.
func transformRecords(b []byte, s Settings) ([]byte, int, error) {
	fs, err := decodeMessage(b)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	result := make([]byte, 0, len(b))
	for _, record := range fs {
		replacement := record.encoded
		if record.kind == 2 && (record.number == 2 || record.number == 22 || record.number == 24) {
			children, err := decodeMessage(record.data)
			if err == nil {
				wifi := record.number == 2
				recognized := !wifi
				for _, child := range children {
					if wifi && child.number == 1 && child.kind == 2 && macAddress.Match(child.data) {
						recognized = true
					}
				}
				if recognized {
					nested := make([]byte, 0, len(record.data))
					count := 0
					for _, child := range children {
						if child.kind == 2 && (wifi && child.number == 2 || !wifi && child.number == 5) {
							updated, changed, updateError := replaceCoordinates(child.data, s)
							if updateError == nil && changed > 0 {
								nested = append(nested, encodeField(child, updated)...)
								count += changed
								continue
							}
						}
						nested = append(nested, child.encoded...)
					}
					if count > 0 {
						replacement = encodeField(record, nested)
						total += count
					}
				}
			}
		}
		result = append(result, replacement...)
	}
	return result, total, nil
}

// patchBody recognizes ARPC or marker envelopes. It deliberately rejects
// speculative offset scans rather than modifying an unidentified protocol.
func patchBody(input []byte, settings Settings) ([]byte, int, error) {
	if err := settings.Validate(); err != nil {
		return nil, 0, err
	}
	if len(input) > bodyLimit {
		return nil, 0, errors.New("compressed response exceeds budget")
	}
	body := input
	if bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		expanded, err := io.ReadAll(io.LimitReader(reader, bodyLimit+1))
		_ = reader.Close()
		if err != nil {
			return nil, 0, err
		}
		if len(expanded) > bodyLimit {
			return nil, 0, errors.New("expanded response exceeds budget")
		}
		body = expanded
	}
	type envelope struct{ lengthAt, width, from, to int }
	frames := make([]envelope, 0, 3)
	if len(body) >= 16 {
		cursor := 2
		valid := true
		for i := 0; i < 3; i++ {
			if cursor+2 > len(body) {
				valid = false
				break
			}
			size := int(binary.BigEndian.Uint16(body[cursor:]))
			cursor += 2
			if size > len(body)-cursor {
				valid = false
				break
			}
			cursor += size
		}
		if valid && cursor+8 <= len(body) {
			from := cursor + 8
			size := uint64(binary.BigEndian.Uint32(body[cursor+4 : from]))
			if size > 0 && size <= uint64(len(body)-from) {
				frames = append(frames, envelope{cursor + 4, 4, from, from + int(size)})
			}
		}
	}
	// Fixed eight-byte envelope used by the older response format.
	if len(body) >= 10 && bytes.Equal(body[:8], []byte{0, 1, 0, 0, 0, 1, 0, 0}) {
		size := int(binary.BigEndian.Uint16(body[8:10]))
		if size > 0 && size <= len(body)-10 {
			frames = append(frames, envelope{8, 2, 10, 10 + size})
		}
	}
	marker := []byte{0, 0, 0, 1, 0, 0}
	// Search only the small envelope header, never the full protobuf body.
	if pos := bytes.Index(body[:min(len(body), 96)], marker); pos >= 0 && pos+8 <= len(body) {
		at := pos + 6
		size := int(binary.BigEndian.Uint16(body[at : at+2]))
		if size > 0 && size <= len(body)-at-2 {
			frames = append(frames, envelope{at, 2, at + 2, at + 2 + size})
		}
	}
	for _, frame := range frames {
		payload, count, err := transformRecords(body[frame.from:frame.to], settings)
		if err != nil || count == 0 || frame.width == 2 && len(payload) > 65535 {
			continue
		}
		result := append([]byte(nil), body[:frame.lengthAt]...)
		if frame.width == 2 {
			result = binary.BigEndian.AppendUint16(result, uint16(len(payload)))
		} else {
			result = binary.BigEndian.AppendUint32(result, uint32(len(payload)))
		}
		result = append(result, payload...)
		result = append(result, body[frame.to:]...)
		return result, count, nil
	}
	return nil, 0, fmt.Errorf("unrecognized location response envelope")
}
