// Copyright 2021 NobeKanai. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package ui // import "miniflux.app/v2/internal/ui"

import (
	"net/http"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response/html"
	"miniflux.app/v2/internal/http/route"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/ui/session"
	"miniflux.app/v2/internal/ui/view"
)

func (h *handler) showCategoryEntriesStarredPage(w http.ResponseWriter, r *http.Request) {
	user, err := h.store.UserByID(request.UserID(r))
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	categoryID := request.RouteInt64Param(r, "categoryID")
	category, err := h.store.Category(request.UserID(r), categoryID)
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	if category == nil {
		html.NotFound(w, r)
		return
	}

	offset := request.QueryIntParam(r, "offset", 0)
	builder := h.store.NewEntryQueryBuilder(user.ID)
	builder.WithCategoryID(category.ID)
	builder.WithStarred(true)
	builder.WithSorting(model.DefaultSortingOrder, "DESC")
	builder.WithoutStatus(model.EntryStatusRemoved)
	builder.WithOffset(offset)
	builder.WithLimit(user.EntriesPerPage)

	entries, err := builder.GetEntries()
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	count, err := builder.CountEntries()
	if err != nil {
		html.ServerError(w, r, err)
		return
	}

	sess := session.New(h.store, request.SessionID(r))
	view := view.New(h.tpl, r, sess)
	view.Set("category", category)
	view.Set("total", count)
	view.Set("entries", entries)
	view.Set("pagination", getPagination(route.Path(h.router, "categoryEntries", "categoryID", category.ID), count, offset, user.EntriesPerPage))
	view.Set("menu", "categories")
	view.Set("user", user)
	view.Set("countUnread", h.store.CountUnreadEntries(user.ID))
	view.Set("countErrorFeeds", h.store.CountUserFeedsWithErrors(user.ID))
	view.Set("hasSaveEntry", h.store.HasSaveEntry(user.ID))
	view.Set("showStarredEntries", true)

	html.OK(w, r, view.Render("category_entries"))
}
