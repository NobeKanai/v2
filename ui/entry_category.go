// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/ui"

import (
	"fmt"
	"net/http"
	"time"

	"miniflux.app/http/request"
	"miniflux.app/http/response/html"
	"miniflux.app/http/route"
	"miniflux.app/model"
	"miniflux.app/storage"
	"miniflux.app/ui/session"
	"miniflux.app/ui/view"
)

func (h *handler) showCategoryEntryPage(w http.ResponseWriter, r *http.Request) {
	user, err := h.store.UserByID(request.UserID(r))
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	categoryID := request.RouteInt64Param(r, "categoryID")
	entryID := request.RouteInt64Param(r, "entryID")

	builder := h.store.NewEntryQueryBuilder(user.ID)
	builder.WithCategoryID(categoryID)
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

	if entry.Status == model.EntryStatusUnread {
		err = h.store.SetEntriesStatus(user.ID, []int64{entry.ID}, model.EntryStatusRead)
		if err != nil {
			html.ServerError(w, r, err)
			return
		}

		entry.Status = model.EntryStatusRead
	}

	var unreadBefore *time.Time
	showOnlyUnread := request.QueryBooleanParam(r, "unread")
	showStarred := request.QueryBooleanParam(r, "starred")

	direction := user.EntryDirection
	if showStarred {
		direction = "desc"
	}

	entryPaginationBuilder := storage.NewEntryPaginationBuilder(h.store, user.ID, entry.ID, user.EntryOrder, direction)
	entryPaginationBuilder.WithCategoryID(categoryID)
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
		nextEntryRoute = route.Path(h.router, "categoryEntry", "categoryID", categoryID, "entryID", nextEntry.ID)
	}

	prevEntryRoute := ""
	if prevEntry != nil {
		prevEntryRoute = route.Path(h.router, "categoryEntry", "categoryID", categoryID, "entryID", prevEntry.ID)
	}

	sess := session.New(h.store, request.SessionID(r))
	view := view.New(h.tpl, r, sess)
	view.Set("entry", entry)
	view.Set("prevEntry", prevEntry)
	view.Set("nextEntry", nextEntry)
	view.Set("nextEntryRoute", nextEntryRoute)
	view.Set("prevEntryRoute", prevEntryRoute)
	view.Set("menu", "categories")
	view.Set("user", user)
	view.Set("countUnread", h.store.CountUnreadEntries(user.ID))
	view.Set("countErrorFeeds", h.store.CountUserFeedsWithErrors(user.ID))
	view.Set("hasSaveEntry", h.store.HasSaveEntry(user.ID))
	view.Set("showOnlyUnreadEntries", showOnlyUnread)
	view.Set("unreadBefore", unreadBefore)
	view.Set("showStarredEntries", showStarred)

	html.OK(w, r, view.Render("entry"))
}
