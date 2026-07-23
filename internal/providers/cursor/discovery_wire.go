package cursor

import (
	"fmt"
)

type modelParameter struct {
	ID    string
	Value string
}

type parameterizedVariant struct {
	Parameters                  []modelParameter
	DisplayName                 string
	DisplayNameOutsidePicker    string
	IsMaxMode                   bool
	IsDefaultMaxConfig          bool
	IsDefaultNonMaxConfig       bool
	VariantStringRepresentation string
}

type parameterizedModel struct {
	Name                        string
	ClientDisplayName           string
	ServerModelName             string
	SupportsImages              bool
	SupportsMaxMode             bool
	SupportsNonMaxMode          bool
	ContextTokenLimit           int
	ContextTokenLimitForMaxMode int
	Variants                    []parameterizedVariant
}

// EncodeAvailableModelsRequest builds aiserver.v1.AvailableModelsRequest.
func EncodeAvailableModelsRequest() []byte {
	// optional bool use_model_parameters = 5;
	// optional bool do_not_use_markdown = 7;
	return []byte{0x28, 0x01, 0x38, 0x01}
}

type wireReader struct {
	data   []byte
	offset int
}

func (r *wireReader) readVarint() (uint64, error) {
	var result uint64
	var shift uint
	for r.offset < len(r.data) {
		b := r.data[r.offset]
		r.offset++
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
		if shift >= 70 {
			return 0, fmt.Errorf("cursor: varint too long")
		}
	}
	return 0, fmt.Errorf("cursor: unexpected EOF while reading varint")
}

func (r *wireReader) readLengthDelimited() ([]byte, error) {
	length, err := r.readVarint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(r.data)-r.offset) {
		return nil, fmt.Errorf("cursor: length-delimited field exceeds buffer")
	}
	end := r.offset + int(length)
	value := r.data[r.offset:end]
	r.offset = end
	return value, nil
}

func (r *wireReader) skipField(wireType int) error {
	switch wireType {
	case 0:
		_, err := r.readVarint()
		return err
	case 1:
		if r.offset+8 > len(r.data) {
			return fmt.Errorf("cursor: fixed64 field exceeds buffer")
		}
		r.offset += 8
		return nil
	case 2:
		_, err := r.readLengthDelimited()
		return err
	case 5:
		if r.offset+4 > len(r.data) {
			return fmt.Errorf("cursor: fixed32 field exceeds buffer")
		}
		r.offset += 4
		return nil
	default:
		return fmt.Errorf("cursor: unsupported wire type %d", wireType)
	}
}

func decodeModelParameter(data []byte) modelParameter {
	reader := &wireReader{data: data}
	parameter := modelParameter{}
	for reader.offset < len(reader.data) {
		tag, err := reader.readVarint()
		if err != nil {
			break
		}
		fieldNo := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch {
		case fieldNo == 1 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			parameter.ID = string(value)
		case fieldNo == 2 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			parameter.Value = string(value)
		default:
			_ = reader.skipField(wireType)
		}
	}
	return parameter
}

func decodeParameterizedVariant(data []byte) parameterizedVariant {
	reader := &wireReader{data: data}
	variant := parameterizedVariant{}
	for reader.offset < len(reader.data) {
		tag, err := reader.readVarint()
		if err != nil {
			break
		}
		fieldNo := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch {
		case fieldNo == 1 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			variant.Parameters = append(variant.Parameters, decodeModelParameter(value))
		case fieldNo == 2 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			variant.DisplayName = string(value)
		case fieldNo == 8 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			variant.DisplayNameOutsidePicker = string(value)
		case fieldNo == 3 && wireType == 0:
			v, _ := reader.readVarint()
			variant.IsMaxMode = v != 0
		case fieldNo == 4 && wireType == 0:
			v, _ := reader.readVarint()
			variant.IsDefaultMaxConfig = v != 0
		case fieldNo == 5 && wireType == 0:
			v, _ := reader.readVarint()
			variant.IsDefaultNonMaxConfig = v != 0
		case fieldNo == 9 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			variant.VariantStringRepresentation = string(value)
		default:
			_ = reader.skipField(wireType)
		}
	}
	return variant
}

func decodeParameterizedModel(data []byte) parameterizedModel {
	reader := &wireReader{data: data}
	model := parameterizedModel{}
	for reader.offset < len(reader.data) {
		tag, err := reader.readVarint()
		if err != nil {
			break
		}
		fieldNo := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch {
		case fieldNo == 1 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			model.Name = string(value)
		case fieldNo == 10 && wireType == 0:
			v, _ := reader.readVarint()
			model.SupportsImages = v != 0
		case fieldNo == 14 && wireType == 0:
			v, _ := reader.readVarint()
			model.SupportsMaxMode = v != 0
		case fieldNo == 19 && wireType == 0:
			v, _ := reader.readVarint()
			model.SupportsNonMaxMode = v != 0
		case fieldNo == 15 && wireType == 0:
			v, _ := reader.readVarint()
			model.ContextTokenLimit = int(v)
		case fieldNo == 16 && wireType == 0:
			v, _ := reader.readVarint()
			model.ContextTokenLimitForMaxMode = int(v)
		case fieldNo == 17 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			model.ClientDisplayName = string(value)
		case fieldNo == 18 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			model.ServerModelName = string(value)
		case fieldNo == 30 && wireType == 2:
			value, _ := reader.readLengthDelimited()
			model.Variants = append(model.Variants, decodeParameterizedVariant(value))
		default:
			_ = reader.skipField(wireType)
		}
	}
	return model
}

// DecodeAvailableModelsResponse parses aiserver.v1.AvailableModelsResponse.
func DecodeAvailableModelsResponse(payload []byte) []parameterizedModel {
	reader := &wireReader{data: payload}
	models := make([]parameterizedModel, 0)
	for reader.offset < len(reader.data) {
		tag, err := reader.readVarint()
		if err != nil {
			break
		}
		fieldNo := int(tag >> 3)
		wireType := int(tag & 0x7)
		if fieldNo == 2 && wireType == 2 {
			value, err := reader.readLengthDelimited()
			if err != nil {
				continue
			}
			model := decodeParameterizedModel(value)
			if model.Name != "" {
				models = append(models, model)
			}
			continue
		}
		_ = reader.skipField(wireType)
	}
	return models
}

func decodeAvailableModelsConnectBody(body []byte) ([]parameterizedModel, error) {
	offset := 0
	var models []parameterizedModel
	for offset < len(body) {
		frame := ParseConnectRPCFrame(body[offset:])
		if frame == nil {
			break
		}
		offset += frame.Consumed
		payload := DecompressPayload(frame.Payload, frame.Flags)
		if len(payload) > 0 && payload[0] == '{' {
			return nil, fmt.Errorf("cursor: available models returned JSON error")
		}
		decoded := DecodeAvailableModelsResponse(payload)
		if len(decoded) > 0 {
			models = append(models, decoded...)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("cursor: empty available models response")
	}
	return models, nil
}
