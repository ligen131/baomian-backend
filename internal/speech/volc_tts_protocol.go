package speech

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const (
	volcTTSMessageFullClient  = 0x1
	volcTTSMessageFullServer  = 0x9
	volcTTSMessageAudioServer = 0xB
	volcTTSMessageError       = 0xF
	volcTTSFlagWithEvent      = 0x4

	volcTTSEventConnectionStarted  = 50
	volcTTSEventConnectionFailed   = 51
	volcTTSEventConnectionFinished = 52
	volcTTSEventSessionFinished    = 152
	volcTTSEventSessionFailed      = 153
)

type volcTTSResponse struct {
	Event        int32
	Sequence     int32
	SessionID    string
	ConnectionID string
	Audio        []byte
	Payload      map[string]any
	Code         string
}

func encodeVolcTTSRequest(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	frame := append([]byte(nil), volcHeader(volcTTSMessageFullClient, volcFlagNoSequence, volcSerializationJSON, volcCompressionNone)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	return append(frame, payload...), nil
}

func parseVolcTTSResponse(frame []byte) (volcTTSResponse, error) {
	if len(frame) < 4 {
		return volcTTSResponse{}, errInvalidVolcFrame
	}
	headerBytes := int(frame[0]&0x0F) * 4
	if headerBytes < 4 || len(frame) < headerBytes {
		return volcTTSResponse{}, errInvalidVolcFrame
	}
	messageType := frame[1] >> 4
	flags := frame[1] & 0x0F
	serialization := frame[2] >> 4
	compression := frame[2] & 0x0F
	payload := frame[headerBytes:]
	response := volcTTSResponse{}

	if messageType == volcTTSMessageError {
		if len(payload) < 4 {
			return volcTTSResponse{}, errInvalidVolcFrame
		}
		response.Code = fmt.Sprint(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
	} else if flags == volcFlagSequence || flags == volcFlagLastSequence {
		if len(payload) < 4 {
			return volcTTSResponse{}, errInvalidVolcFrame
		}
		response.Sequence = int32(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
	}

	if flags == volcTTSFlagWithEvent {
		if len(payload) < 4 {
			return volcTTSResponse{}, errInvalidVolcFrame
		}
		response.Event = int32(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
		if !volcTTSConnectionEvent(response.Event) {
			var err error
			response.SessionID, payload, err = consumeVolcString(payload)
			if err != nil {
				return volcTTSResponse{}, err
			}
		}
		if volcTTSConnectionResultEvent(response.Event) {
			var err error
			response.ConnectionID, payload, err = consumeVolcString(payload)
			if err != nil {
				return volcTTSResponse{}, err
			}
		}
	}

	var err error
	payload, err = sizedPayload(payload)
	if err != nil {
		return volcTTSResponse{}, err
	}
	if compression == volcCompressionGZIP {
		payload, err = gunzipBytes(payload)
		if err != nil {
			return volcTTSResponse{}, errInvalidVolcFrame
		}
	} else if compression != volcCompressionNone {
		return volcTTSResponse{}, fmt.Errorf("%w: compression %d", errInvalidVolcFrame, compression)
	}

	if messageType == volcTTSMessageAudioServer {
		response.Audio = append([]byte(nil), payload...)
		return response, nil
	}
	if messageType != volcTTSMessageFullServer && messageType != volcTTSMessageError {
		return volcTTSResponse{}, fmt.Errorf("%w: message type %d", errInvalidVolcFrame, messageType)
	}
	if len(payload) == 0 {
		return response, nil
	}
	if serialization != volcSerializationJSON {
		return volcTTSResponse{}, fmt.Errorf("%w: serialization %d", errInvalidVolcFrame, serialization)
	}
	if err := json.Unmarshal(payload, &response.Payload); err != nil {
		return volcTTSResponse{}, fmt.Errorf("%w: JSON payload", errInvalidVolcFrame)
	}
	return response, nil
}

func volcTTSConnectionEvent(event int32) bool {
	return event == volcTTSEventConnectionStarted ||
		event == volcTTSEventConnectionFailed ||
		event == volcTTSEventConnectionFinished
}

func volcTTSConnectionResultEvent(event int32) bool {
	return volcTTSConnectionEvent(event)
}

func appendVolcString(destination []byte, value string) []byte {
	destination = binary.BigEndian.AppendUint32(destination, uint32(len(value)))
	return append(destination, value...)
}

func consumeVolcString(value []byte) (string, []byte, error) {
	if len(value) < 4 {
		return "", nil, errInvalidVolcFrame
	}
	size := int(binary.BigEndian.Uint32(value[:4]))
	if size < 0 || len(value) < 4+size {
		return "", nil, errInvalidVolcFrame
	}
	return string(value[4 : 4+size]), value[4+size:], nil
}
