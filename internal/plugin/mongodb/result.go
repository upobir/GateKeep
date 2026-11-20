package mongodb

import (
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func extJSON(value any) (any, error) {
	// Extended JSON writes a document at the top. An array is wrapped, then unwrapped.
	payload := value
	array := false
	switch value.(type) {
	case bson.A, []any:
		payload = bson.D{{Key: "v", Value: value}}
		array = true
	}
	text, err := bson.MarshalExtJSON(payload, false, false)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(text, &out); err != nil {
		return nil, err
	}
	if !array {
		return out, nil
	}
	wrapped, ok := out.(map[string]any)
	if !ok {
		return nil, errors.New("could not format the result")
	}
	return wrapped["v"], nil
}

func documents(raws []bson.Raw) (any, error) {
	out := make([]any, 0, len(raws))
	for _, raw := range raws {
		value, err := extJSON(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

// commandReply is the server reply without replication metadata.
func commandReply(raw bson.Raw) (any, error) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, err
	}
	cleaned := make(bson.D, 0, len(elements))
	for _, element := range elements {
		switch element.Key() {
		case "$clusterTime", "operationTime":
			continue
		default:
			cleaned = append(cleaned, bson.E{Key: element.Key(), Value: element.Value()})
		}
	}
	return extJSON(cleaned)
}
