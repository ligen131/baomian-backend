package speech

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	volcProtocolVersion = 1
	volcHeaderWords     = 1

	volcMessageFullClient  = 0x1
	volcMessageAudioClient = 0x2
	volcMessageFullServer  = 0x9
	volcMessageServerACK   = 0xB
	volcMessageError       = 0xF

	volcFlagNoSequence   = 0x0
	volcFlagSequence     = 0x1
	volcFlagLast         = 0x2
	volcFlagLastSequence = 0x3

	volcSerializationNone = 0x0
	volcSerializationJSON = 0x1
	volcCompressionNone   = 0x0
	volcCompressionGZIP   = 0x1
)

var errInvalidVolcFrame = errors.New("invalid Volcengine speech frame")

type volcASRResponse struct {
	Sequence int32
	Last     bool
	Code     uint32
	Payload  map[string]any
}

func volcHeader(messageType, flags, serialization, compression byte) []byte {
	return []byte{
		volcProtocolVersion<<4 | volcHeaderWords,
		messageType<<4 | flags,
		serialization<<4 | compression,
		0,
	}
}

func encodeVolcASRFullRequest(sequence int32, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	payload, err = gzipBytes(payload)
	if err != nil {
		return nil, err
	}
	frame := append([]byte(nil), volcHeader(volcMessageFullClient, volcFlagSequence, volcSerializationJSON, volcCompressionGZIP)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(sequence))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	return append(frame, payload...), nil
}

func encodeVolcASRAudio(audio []byte, last bool) ([]byte, error) {
	payload, err := gzipBytes(audio)
	if err != nil {
		return nil, err
	}
	flags := byte(volcFlagNoSequence)
	if last {
		flags = volcFlagLast
	}
	frame := append([]byte(nil), volcHeader(volcMessageAudioClient, flags, volcSerializationNone, volcCompressionGZIP)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	return append(frame, payload...), nil
}

func parseVolcASRResponse(frame []byte) (volcASRResponse, error) {
	if len(frame) < 4 {
		return volcASRResponse{}, errInvalidVolcFrame
	}
	headerBytes := int(frame[0]&0x0F) * 4
	if headerBytes < 4 || len(frame) < headerBytes {
		return volcASRResponse{}, errInvalidVolcFrame
	}
	messageType := frame[1] >> 4
	flags := frame[1] & 0x0F
	serialization := frame[2] >> 4
	compression := frame[2] & 0x0F
	payload := frame[headerBytes:]
	response := volcASRResponse{Last: flags&volcFlagLast != 0}

	if flags&volcFlagSequence != 0 {
		if len(payload) < 4 {
			return volcASRResponse{}, errInvalidVolcFrame
		}
		response.Sequence = int32(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
	}

	switch messageType {
	case volcMessageFullServer:
		var err error
		payload, err = sizedPayload(payload)
		if err != nil {
			return volcASRResponse{}, err
		}
	case volcMessageServerACK:
		if len(payload) < 4 {
			return volcASRResponse{}, errInvalidVolcFrame
		}
		response.Sequence = int32(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
		if len(payload) == 0 {
			return response, nil
		}
		var err error
		payload, err = sizedPayload(payload)
		if err != nil {
			return volcASRResponse{}, err
		}
	case volcMessageError:
		if len(payload) < 8 {
			return volcASRResponse{}, errInvalidVolcFrame
		}
		response.Code = binary.BigEndian.Uint32(payload[:4])
		var err error
		payload, err = sizedPayload(payload[4:])
		if err != nil {
			return volcASRResponse{}, err
		}
	default:
		return volcASRResponse{}, fmt.Errorf("%w: message type %d", errInvalidVolcFrame, messageType)
	}

	var err error
	if compression == volcCompressionGZIP {
		payload, err = gunzipBytes(payload)
		if err != nil {
			return volcASRResponse{}, fmt.Errorf("%w: gzip payload", errInvalidVolcFrame)
		}
	} else if compression != volcCompressionNone {
		return volcASRResponse{}, fmt.Errorf("%w: compression %d", errInvalidVolcFrame, compression)
	}
	if len(payload) == 0 {
		return response, nil
	}
	if serialization != volcSerializationJSON {
		return volcASRResponse{}, fmt.Errorf("%w: serialization %d", errInvalidVolcFrame, serialization)
	}
	if err := json.Unmarshal(payload, &response.Payload); err != nil {
		return volcASRResponse{}, fmt.Errorf("%w: JSON payload", errInvalidVolcFrame)
	}
	return response, nil
}

func sizedPayload(value []byte) ([]byte, error) {
	if len(value) < 4 {
		return nil, errInvalidVolcFrame
	}
	size := int(binary.BigEndian.Uint32(value[:4]))
	if size < 0 || len(value)-4 != size {
		return nil, errInvalidVolcFrame
	}
	return value[4:], nil
}

func gzipBytes(value []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(value); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func gunzipBytes(value []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(value))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
