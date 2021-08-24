// Copyright 2017 Frédéric Guillot. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package storage // import "miniflux.app/storage"

import (
	"fmt"
	"math"
	"math/rand"

	"miniflux.app/config"
	"miniflux.app/model"
)

// NewBatch returns a series of jobs.
func (s *Storage) NewBatch(batchSize int) (jobs model.JobList, err error) {
	pollingParsingErrorLimit := config.Opts.PollingParsingErrorLimit()
	query := `
		SELECT
			id,
			user_id
		FROM
			feeds
		WHERE
			disabled is false AND next_check_at < now() AND 
			CASE WHEN $1 > 0 THEN parsing_error_count < $1 ELSE parsing_error_count >= 0 END
		ORDER BY next_check_at ASC LIMIT $2
	`
	return s.fetchBatchRows(query, pollingParsingErrorLimit, batchSize)
}

// NewUserBatch returns a series of jobs but only for a given user.
func (s *Storage) NewUserBatch(userID int64, batchSize int) (jobs model.JobList, err error) {
	// We do not take the error counter into consideration when the given
	// user refresh manually all his feeds to force a refresh.
	query := `
		SELECT
			id,
			user_id
		FROM
			feeds
		WHERE
			user_id=$1 AND disabled is false
		ORDER BY next_check_at ASC LIMIT %d
	`
	return s.fetchBatchRows(fmt.Sprintf(query, batchSize), userID)
}

func (s *Storage) NewFrequencyBasedRandomedBatch(batchSize int) (jobs model.JobList, err error) {
	pollingParsingErrorLimit := config.Opts.PollingParsingErrorLimit()
	query := `
		SELECT
			id,
			user_id
		FROM
			feeds
		WHERE
			disabled is false AND
			CASE WHEN $1 > 0 THEN parsing_error_count < $1 ELSE parsing_error_count >= 0 END
		ORDER BY checked_at ASC
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
		if err := rows.Scan(&job.FeedID, &job.UserID); err != nil {
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
// otherwise it will ensure that the expected value of the probability in four hours is 1.0
// The longer it has not been updated, the higher the probability of being updated.
func (s *Storage) feedRefreshProbability(j *model.Job) (float64, error) {
	const gradient float64 = 5 / 102.0
	var weight float64 = 1 / 3.0

	countWeekly, err := s.WeeklyFeedOneHourBeforeAndAfterCount(j.UserID, j.FeedID)
	if err != nil {
		return 0, err
	}
	hours, err := s.FeedHoursSinceLastCheck(j.UserID, j.FeedID)
	if err != nil {
		return 0, err
	}

	if countWeekly != 0 {
		weight = float64(countWeekly) * (1 + math.Pow(8, hours)/256)
	} else {
		weight += gradient * hours
	}

	feedAge, err := s.FeedAgeDays(j.UserID, j.FeedID)
	if err != nil {
		return 0, err
	}
	feedAge = math.Max(1.0, feedAge)
	feedAge = math.Min(7.0, feedAge)

	return weight / feedAge, nil
}

func isHit(probability float64) bool {
	return rand.Float64() < probability
}
