package processor

import (
	"encoding/json"
	"fmt"

	"miniflux.app/model"
	"rogchap.com/v8go"
)

var (
	iso, _ = v8go.NewIsolate()
)

// rewriteEntries rewrite entires with custom script.
func rewriteEntries(feed *model.Feed) error {
	var filteredEntries model.Entries

	ctx, _ := v8go.NewContext(iso)
	defer ctx.Close()

	objJson, _ := json.Marshal(feed.Entries)
	ctx.RunScript(fmt.Sprintf("let entries = %s;", objJson), "rewrite.js")

	_, err := ctx.RunScript(feed.CustomScript, "rewrite.js")
	if err != nil {
		return err
	}

	objValue, err := ctx.RunScript("entries", "rewrite.js")
	if err != nil {
		return err
	}

	objJson, _ = objValue.MarshalJSON()
	err = json.Unmarshal(objJson, &filteredEntries)
	if err != nil {
		return err
	}

	feed.Entries = filteredEntries
	return nil
}
