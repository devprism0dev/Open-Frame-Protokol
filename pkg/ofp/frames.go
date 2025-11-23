package ofp

import (
	"github.com/sirupsen/logrus"
)

// Frame type constants
const (
	FrameTypeData        = 0x01 // Data frame
	FrameTypePing        = 0x02 // Ping (heartbeat request)
	FrameTypePong        = 0x03 // Pong (heartbeat response)
	FrameTypeSubscribe   = 0x04 // Subscribe to topic
	FrameTypeUnsubscribe = 0x05 // Unsubscribe from topic
	FrameTypePublish     = 0x06 // Publish to topic
	FrameTypeEmitEvent   = 0x07 // Emit event
	FrameTypeOnEvent     = 0x08 // Listen to event
	FrameTypeOffEvent    = 0x09 // Stop listening to event
)

// CreateDataFrame creates a data frame with the given payload
func CreateDataFrame(data []byte) []byte {
	frame := make([]byte, 3+len(data))
	frame[0] = FrameTypeData
	frame[1] = byte(len(data) >> 8)
	frame[2] = byte(len(data) & 0xFF)
	copy(frame[3:], data)
	return frame
}

// CreatePingFrame creates a ping frame
func CreatePingFrame() []byte {
	return []byte{FrameTypePing, 0x00, 0x00}
}

// CreatePongFrame creates a pong frame
func CreatePongFrame() []byte {
	return []byte{FrameTypePong, 0x00, 0x00}
}

// ParseFrame parses a frame and returns frame type and payload
func ParseFrame(frame []byte) (frameType byte, payload []byte) {
	if len(frame) < 3 {
		return 0, nil
	}

	frameType = frame[0]
	if frameType == FrameTypeData && len(frame) > 3 {
		length := int(frame[1])<<8 | int(frame[2])
		if len(frame) >= 3+length {
			payload = frame[3 : 3+length]
		}
	}
	return frameType, payload
}

// ParseTopicFrame parses a frame with topic (Subscribe, Unsubscribe, Publish)
// Format: [FrameType][TopicLength(2)][Topic][DataLength(2)][Data]
func ParseTopicFrame(frame []byte) (frameType byte, topic string, data []byte) {
	if len(frame) < 5 {
		return 0, "", nil
	}

	frameType = frame[0]
	topicLength := int(frame[1])<<8 | int(frame[2])
	if len(frame) < 3+topicLength {
		return frameType, "", nil
	}

	topic = string(frame[3 : 3+topicLength])
	offset := 3 + topicLength

	if len(frame) < offset+2 {
		return frameType, topic, nil
	}

	dataLength := int(frame[offset])<<8 | int(frame[offset+1])
	if len(frame) >= offset+2+dataLength {
		data = frame[offset+2 : offset+2+dataLength]
	}

	return frameType, topic, data
}

// CreateTopicFrame creates a frame with topic (Subscribe, Unsubscribe, Publish)
// Format: [FrameType][TopicLength(2)][Topic][DataLength(2)][Data]
func CreateTopicFrame(frameType byte, topic string, data []byte) []byte {
	topicBytes := []byte(topic)
	topicLen := len(topicBytes)
	dataLen := len(data)

	frame := make([]byte, 5+topicLen+dataLen)
	frame[0] = frameType
	frame[1] = byte(topicLen >> 8)
	frame[2] = byte(topicLen & 0xFF)
	copy(frame[3:], topicBytes)
	frame[3+topicLen] = byte(dataLen >> 8)
	frame[3+topicLen+1] = byte(dataLen & 0xFF)
	if dataLen > 0 {
		copy(frame[3+topicLen+2:], data)
	}

	return frame
}

// ParseEventFrame parses an event frame (EmitEvent, OnEvent, OffEvent)
// Format: [FrameType][EventNameLength(2)][EventName][DataLength(2)][Data]
func ParseEventFrame(frame []byte) (frameType byte, eventName string, data []byte) {
	if len(frame) < 5 {
		return 0, "", nil
	}

	frameType = frame[0]
	eventNameLength := int(frame[1])<<8 | int(frame[2])
	if len(frame) < 3+eventNameLength {
		return frameType, "", nil
	}

	eventName = string(frame[3 : 3+eventNameLength])
	offset := 3 + eventNameLength

	if len(frame) < offset+2 {
		return frameType, eventName, nil
	}

	dataLength := int(frame[offset])<<8 | int(frame[offset+1])
	if len(frame) >= offset+2+dataLength {
		data = frame[offset+2 : offset+2+dataLength]
	}

	return frameType, eventName, data
}

// CreateEventFrame creates an event frame (EmitEvent, OnEvent, OffEvent)
// Format: [FrameType][EventNameLength(2)][EventName][DataLength(2)][Data]
func CreateEventFrame(frameType byte, eventName string, data []byte) []byte {
	eventNameBytes := []byte(eventName)
	eventNameLen := len(eventNameBytes)
	dataLen := len(data)

	frame := make([]byte, 5+eventNameLen+dataLen)
	frame[0] = frameType
	frame[1] = byte(eventNameLen >> 8)
	frame[2] = byte(eventNameLen & 0xFF)
	copy(frame[3:], eventNameBytes)
	frame[3+eventNameLen] = byte(dataLen >> 8)
	frame[3+eventNameLen+1] = byte(dataLen & 0xFF)
	if dataLen > 0 {
		copy(frame[3+eventNameLen+2:], data)
	}

	return frame
}

// WriteDataFrame writes a data frame to the stream with logging
func WriteDataFrame(stream *Stream, data []byte) error {
	frame := CreateDataFrame(data)
	_, err := stream.Write(frame)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"component": "handler",
			"action":    "send_response",
			"error":     err,
		}).Error("Failed to send echo response")
		return err
	}

	logrus.WithFields(logrus.Fields{
		"component":     "handler",
		"action":        "send_response",
		"response_size": len(data),
		"data":          string(data),
	}).Debug("Sent echo response to client")
	return nil
}
