// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"fmt"
	"net/http"
	"time"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response/html"
	"miniflux.app/v2/internal/http/route"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/ui/session"
	"miniflux.app/v2/internal/ui/view"
)

func (h *handler) showFeedEntryPage(w http.ResponseWriter, r *http.Request) {
	user, err := h.store.UserByID(request.UserID(r))
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	entryID := request.RouteInt64Param(r, "entryID")
	feedID := request.RouteInt64Param(r, "feedID")

	builder := h.store.NewEntryQueryBuilder(user.ID)
	builder.WithFeedID(feedID)
	builder.WithEntryID(entryID)
	builder.WithoutStatus(model.EntryStatusRemoved)

	entry, err := builder.GetEntry()
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	if entry == nil {
		html.NotFound(w, r)
		return
	}

	if user.MarkReadOnView && entry.Status == model.EntryStatusUnread {
		err = h.store.SetEntriesStatus(user.ID, []int64{entry.ID}, model.EntryStatusRead)
		if err != nil {
			html.ServerError(w, r, err)
			return
		}

		entry.Status = model.EntryStatusRead
	}

	var unreadBefore *time.Time
	showOnlyUnread := request.QueryBoolParam(r, "unread", false)
	showStarred := request.QueryBoolParam(r, "starred", false)

	direction := user.EntryDirection
	if showStarred {
		direction = "desc"
	}

	entryPaginationBuilder := storage.NewEntryPaginationBuilder(h.store, user.ID, entry.ID, user.EntryOrder, direction)
	entryPaginationBuilder.WithFeedID(feedID)
	if showOnlyUnread {
		unreadBefore = request.QueryTimestampParam(r, "unreadBefore")
		if unreadBefore == nil {
			html.RedirectWithQueries(w, r, "unreadBefore", fmt.Sprint(time.Now().Unix()))
			return
		}
		entryPaginationBuilder.WithUnreadBefore(*unreadBefore)
	} else if showStarred {
		entryPaginationBuilder.WithStarred()
	}

	prevEntry, nextEntry, err := entryPaginationBuilder.Entries()
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	nextEntryRoute := ""
	if nextEntry != nil {
		nextEntryRoute = route.Path(h.router, "feedEntry", "feedID", feedID, "entryID", nextEntry.ID)
	}

	prevEntryRoute := ""
	if prevEntry != nil {
		prevEntryRoute = route.Path(h.router, "feedEntry", "feedID", feedID, "entryID", prevEntry.ID)
	}

	sess := session.New(h.store, request.SessionID(r))
	view := view.New(h.tpl, r, sess)
	view.Set("entry", entry)
	view.Set("prevEntry", prevEntry)
	view.Set("nextEntry", nextEntry)
	view.Set("nextEntryRoute", nextEntryRoute)
	view.Set("prevEntryRoute", prevEntryRoute)
	view.Set("menu", "feeds")
	view.Set("user", user)
	view.Set("countUnread", h.store.CountUnreadEntries(user.ID))
	view.Set("countErrorFeeds", h.store.CountUserFeedsWithErrors(user.ID))
	view.Set("hasSaveEntry", h.store.HasSaveEntry(user.ID))
	view.Set("showOnlyUnreadEntries", showOnlyUnread)
	view.Set("unreadBefore", unreadBefore)
	view.Set("showStarredEntries", showStarred)

	html.OK(w, r, view.Render("entry"))
}
