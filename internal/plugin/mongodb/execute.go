package mongodb

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type command struct {
	doc bson.D
}

func parseCommand(text string) (command, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return command{}, errors.New("command is required")
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON([]byte(text), false, &doc); err != nil || len(doc) == 0 {
		return command{}, errors.New("command must be a JSON document")
	}
	return command{doc: doc}, nil
}

func (cmd command) run(ctx context.Context, client *mongo.Client, cfg connConfig) (any, error) {
	database := client.Database(cfg.database)
	if returnsCursor(cmd.doc) {
		cursor, err := database.RunCommandCursor(ctx, cmd.doc)
		if err != nil {
			return nil, err
		}
		raws, err := readCursor(ctx, cursor)
		if err != nil {
			return nil, err
		}
		return documents(raws)
	}
	raw, err := database.RunCommand(ctx, cmd.doc).Raw()
	if err != nil {
		return nil, err
	}
	return commandReply(raw)
}

func returnsCursor(doc bson.D) bool {
	if len(doc) == 0 {
		return false
	}
	switch doc[0].Key {
	case "find", "aggregate", "listCollections", "listIndexes":
		return true
	default:
		return false
	}
}

func readCursor(ctx context.Context, cursor *mongo.Cursor) ([]bson.Raw, error) {
	var docs []bson.Raw
	for cursor.Next(ctx) {
		docs = append(docs, append(bson.Raw(nil), cursor.Current...))
	}
	err := cursor.Err()
	closeErr := cursor.Close(ctx)
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if docs == nil {
		docs = []bson.Raw{}
	}
	return docs, nil
}
