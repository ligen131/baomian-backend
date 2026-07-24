package speech

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestEncodeVolcASRFullRequest(t *testing.T) {
	frame, err := encodeVolcASRFullRequest(1, map[string]any{"audio": map[string]any{"sample_rate": 24000}})
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != 0x11 || frame[1] != 0x11 || frame[2] != 0x11 {
		t.Fatalf("header = %x", frame[:4])
	}
	if sequence := int32(binary.BigEndian.Uint32(frame[4:8])); sequence != 1 {
		t.Fatalf("sequence = %d", sequence)
	}
	payload, err := sizedPayload(frame[8:])
	if err != nil {
		t.Fatal(err)
	}
	payload, err = gunzipBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	if request["audio"].(map[string]any)["sample_rate"] != float64(24000) {
		t.Fatalf("request = %#v", request)
	}
}

func TestEncodeVolcASRAudioLastPackage(t *testing.T) {
	frame, err := encodeVolcASRAudio([]byte{1, 2, 3}, true)
	if err != nil {
		t.Fatal(err)
	}
	if frame[1] != 0x22 || frame[2] != 0x01 {
		t.Fatalf("header = %x", frame[:4])
	}
	payload, err := sizedPayload(frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	payload, err = gunzipBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("payload = %v", payload)
	}
}

func TestVolcTTSProtocolRequestAndAudio(t *testing.T) {
	frame, err := encodeVolcTTSRequest(map[string]any{"req_params": map[string]any{"text": "你好"}})
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != 0x11 || frame[1] != 0x10 || frame[2] != 0x10 {
		t.Fatalf("header = %x", frame[:4])
	}
	payload, err := sizedPayload(frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"req_params":{"text":"你好"}}` {
		t.Fatalf("payload = %s", payload)
	}

	responseFrame := encodeTestTTSAudio(1, []byte{4, 5, 6})
	response, err := parseVolcTTSResponse(responseFrame)
	if err != nil {
		t.Fatal(err)
	}
	if response.Sequence != 1 || string(response.Audio) != string([]byte{4, 5, 6}) {
		t.Fatalf("response = %#v", response)
	}
}

func encodeTestASRResponse(t *testing.T, sequence int32, last bool, payload map[string]any) []byte {
	t.Helper()
	value, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	value, err = gzipBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	flags := byte(volcFlagSequence)
	if last {
		flags = volcFlagLastSequence
	}
	frame := append([]byte(nil), volcHeader(volcMessageFullServer, flags, volcSerializationJSON, volcCompressionGZIP)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(sequence))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(value)))
	return append(frame, value...)
}

func encodeTestASRError(t *testing.T, code uint32, message string) []byte {
	t.Helper()
	value, err := json.Marshal(map[string]any{"message": message})
	if err != nil {
		t.Fatal(err)
	}
	frame := append([]byte(nil), volcHeader(volcMessageError, volcFlagNoSequence, volcSerializationJSON, volcCompressionNone)...)
	frame = binary.BigEndian.AppendUint32(frame, code)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(value)))
	return append(frame, value...)
}
