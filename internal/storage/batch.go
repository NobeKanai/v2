// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

import (
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
)

type BatchBuilder struct {
	db         *sql.DB
	args       []any
	conditions []string
	limit      int
}

func (s *Storage) NewBatchBuilder() *BatchBuilder {
	return &BatchBuilder{
		db: s.db,
	}
}

func (b *BatchBuilder) WithBatchSize(batchSize int) *BatchBuilder {
	b.limit = batchSize
	return b
}

func (b *BatchBuilder) WithUserID(userID int64) *BatchBuilder {
	b.conditions = append(b.conditions, fmt.Sprintf("user_id = $%d", len(b.args)+1))
	b.args = append(b.args, userID)
	return b
}

func (b *BatchBuilder) WithCategoryID(categoryID int64) *BatchBuilder {
	b.conditions = append(b.conditions, fmt.Sprintf("category_id = $%d", len(b.args)+1))
	b.args = append(b.args, categoryID)
	return b
}

func (b *BatchBuilder) WithErrorLimit(limit int) *BatchBuilder {
	if limit > 0 {
		b.conditions = append(b.conditions, fmt.Sprintf("parsing_error_count < $%d", len(b.args)+1))
		b.args = append(b.args, limit)
	}
	return b
}

func (b *BatchBuilder) WithNextCheckExpired() *BatchBuilder {
	b.conditions = append(b.conditions, "next_check_at < now()")
	return b
}

func (b *BatchBuilder) WithoutDisabledFeeds() *BatchBuilder {
	b.conditions = append(b.conditions, "disabled is false")
	return b
}

func (b *BatchBuilder) FetchJobs() (jobs model.JobList, err error) {
	query := `SELECT id, user_id FROM feeds`

	if len(b.conditions) > 0 {
		query += fmt.Sprintf(" WHERE %s", strings.Join(b.conditions, " AND "))
	}

	if b.limit > 0 {
		query += fmt.Sprintf(" ORDER BY next_check_at ASC LIMIT %d", b.limit)
	}

	rows, err := b.db.Query(query, b.args...)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch batch of jobs: %v`, err)
	}
	defer rows.Close()

	for rows.Next() {
		var job model.Job
		if err := rows.Scan(&job.FeedID, &job.UserID); err != nil {
			return nil, fmt.Errorf(`store: unable to fetch job: %v`, err)
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

func (s *Storage) NewFrequencyBasedRandomedBatch(batchSize int) (jobs model.JobList, err error) {
	pollingParsingErrorLimit := config.Opts.PollingParsingErrorLimit()
	query := `
		SELECT
			f.id,
			f.user_id,
			(
				SELECT count(*)
				FROM entries e, now() AS n, CAST(n AS date) as d, CAST(n AS time) as t
				WHERE e.user_id = f.user_id AND e.feed_id = f.id AND
				e.published_at::date BETWEEN (d - 7) AND (d - 1) AND
				e.published_at::time BETWEEN (t - interval '1 hour') AND (t + interval '1 hour')
			) AS range_count,
			COALESCE(
				(
					SELECT EXTRACT(EPOCH FROM now()-e.published_at)/86400
					FROM entries e
					WHERE e.user_id = f.user_id AND e.feed_id = f.id 
					ORDER BY e.published_at LIMIT 1
				),
				0
			) AS age,
			(
				SELECT EXTRACT(EPOCH FROM now()-f.checked_at)/3600
			) AS last_checked_at
		FROM
			feeds f
		WHERE
			f.disabled is false AND
			CASE WHEN $1 > 0 THEN f.parsing_error_count < $1 ELSE f.parsing_error_count >= 0 END
	`
	var (
		allJobs     model.JobList
		probability float64
	)
	allJobs, err = s.fetchBatchRows(query, pollingParsingErrorLimit)
	for _, j := range allJobs {
		probability, err = s.feedRefreshProbability(&j)
		if err != nil {
			return nil, err
		}

		if isHit(probability) {
			jobs = append(jobs, j)

			if len(jobs) >= batchSize {
				return
			}
		}
	}
	return
}

func (s *Storage) fetchBatchRows(query string, args ...interface{}) (jobs model.JobList, err error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch batch of jobs: %v`, err)
	}
	defer rows.Close()

	for rows.Next() {
		var job model.Job
		if err := rows.Scan(&job.FeedID, &job.UserID, &job.WeeklyFeedOneHourBeforeAndAfterCount, &job.AgeDays, &job.HoursSinceLastCheck); err != nil {
			return nil, fmt.Errorf(`store: unable to fetch job: %v`, err)
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// feedRefreshProbability get the feed's probability that it should be updated based
// on the update frequency in the past 7 days.
// When there are new entries one hour before and after the same time in the past week,
// the probability is that count / 7(this 7 can be smaller if feed's age is smaller than
// 7, but at least 1.0)
// otherwise it will ensure the expected value of the probability in four hours is 1.0.
// The longer it has not been updated, the higher the probability of being updated.
func (s *Storage) feedRefreshProbability(j *model.Job) (float64, error) {
	const gradient float64 = 5 / 102.0
	var weight float64 = 1 / 3.0

	if j.WeeklyFeedOneHourBeforeAndAfterCount != 0 {
		weight = float64(j.WeeklyFeedOneHourBeforeAndAfterCount) * (1 + math.Pow(8, j.HoursSinceLastCheck)/256)
	} else {
		weight += gradient * j.HoursSinceLastCheck
	}

	feedAge := min(7.0, max(1.0, j.AgeDays))

	return weight / feedAge, nil
}

func isHit(probability float64) bool {
	return rand.Float64() < probability
}
