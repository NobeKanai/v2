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

// rewriteEntries rewrite entries with custom script.
func rewriteEntries(feed *model.Feed) error {
	var safeEntries []*safeEntry
	var filteredEntries model.Entries
	var entries = make(map[string]*model.Entry)

	for _, entry := range feed.Entries {
		entries[entry.Hash] = entry
		safeEntries = append(safeEntries, newSafeEntry(entry))
	}

	ctx, _ := v8go.NewContext(iso)
	defer ctx.Close()

	objJson, _ := json.Marshal(safeEntries)
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
	err = json.Unmarshal(objJson, &safeEntries)
	if err != nil {
		return err
	}

	for _, safeEntry := range safeEntries {
		if entry, ok := entries[safeEntry.Hash]; ok {
			safeEntry.merge(entry)
			filteredEntries = append(filteredEntries, entry)
		}
	}

	feed.Entries = filteredEntries
	return nil
}

type safeEntry struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	CommentsURL string `json:"comments_url"`
	Content     string `json:"content"`
	Author      string `json:"author"`
	Hash        string `json:"hash"`
}

func newSafeEntry(entry *model.Entry) *safeEntry {
	return &safeEntry{
		Title:       entry.Title,
		URL:         entry.URL,
		CommentsURL: entry.CommentsURL,
		Content:     entry.Content,
		Author:      entry.Author,
		Hash:        entry.Hash,
	}
}

func (se *safeEntry) merge(entry *model.Entry) {
	entry.Title = se.Title
	entry.URL = se.URL
	entry.CommentsURL = se.CommentsURL
	entry.Content = se.Content
	entry.Author = se.Author
}
