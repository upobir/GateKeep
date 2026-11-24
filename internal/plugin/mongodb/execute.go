package mongodb

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type command struct {
	doc          bson.D
	hasMaxTimeMS bool
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
	hasMaxTimeMS := false
	for _, field := range doc {
		if field.Key == "maxTimeMS" {
			if hasMaxTimeMS {
				return command{}, errors.New("command must not contain duplicate maxTimeMS fields")
			}
			hasMaxTimeMS = true
		}
	}
	return command{doc: doc, hasMaxTimeMS: hasMaxTimeMS}, nil
}

// commandContext hides the deadline from the driver's automatic maxTimeMS
// calculation when we supply an explicit server limit. Done, Err and Value
// still delegate to the original context, preserving Gatekeep cancellation.
type commandContext struct{ context.Context }

func (commandContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (cmd command) run(ctx context.Context, client *mongo.Client, cfg connConfig) (any, error) {
	doc := cmd.doc
	runCtx := ctx
	if cmd.hasMaxTimeMS {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		runCtx = commandContext{ctx}
	}
	database := client.Database(cfg.database)
	if returnsCursor(doc) {
		cursor, err := database.RunCommandCursor(runCtx, doc)
		if err != nil {
			return nil, err
		}
		raws, err := readCursor(ctx, cursor)
		if err != nil {
			return nil, err
		}
		return documents(raws)
	}
	raw, err := database.RunCommand(runCtx, doc).Raw()
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
