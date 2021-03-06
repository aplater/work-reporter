package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	jira "github.com/andygrunwald/go-jira"
)

const (
	dayFormat  = "2006-01-02"
	dateFormat = "2006-01-02T15:04:05Z07:00"
	// We use one week for a sprint
	sprintDuration = 7 * 24 * time.Hour
)

// Get the board ID by project and boardType.
// Here we assume that you must create a board in the project and
// the function will return the first board ID.
func getBoardID(project string, boardType string) int {
	opts := jira.BoardListOptions{
		BoardType:      boardType,
		ProjectKeyOrID: project,
	}

	boards, _, err := jiraClient.Board.GetAllBoards(&opts)
	perror(err)

	return boards.Values[0].ID
}

func getSprints(boardID int, opts jira.GetAllSprintsOptions) []jira.Sprint {
	var allSprints []jira.Sprint

	pos := 0
	for {
		nextOpts := &jira.GetAllSprintsOptions{
			State: opts.State,
			SearchOptions: jira.SearchOptions{
				StartAt:    pos,
				MaxResults: 100,
			},
		}
		results, _, err := jiraClient.Board.GetAllSprintsWithOptions(boardID, nextOpts)
		perror(err)
		allSprints = append(allSprints, results.Values...)

		if results.IsLast {
			break
		}
		pos += len(results.Values)
	}

	return allSprints
}

// Returns the only active sprint
func getActiveSprint(boardID int) jira.Sprint {
	sprints := getSprints(boardID, jira.GetAllSprintsOptions{
		State: "active",
	})
	for _, sprint := range sprints {
		if strings.Contains(sprint.Name, config.Jira.Project) {
			// Only care about current project's sprints.
			return sprint
		}
	}
	return sprints[0]
}

func getLatestPassedSprint(sprints []jira.Sprint) *jira.Sprint {
	now := time.Now()
	minDiff := time.Hour * 7 * 24
	var minSprint *jira.Sprint
	for idx, sprint := range sprints {
		if !strings.Contains(sprint.Name, config.Jira.Project) {
			// Only care about current project's sprints.
			continue
		}
		// 1. Sprint Start Date < Now
		// 2. Sprint End Date < Now
		// 3. Min(Now - Sprint End Date)
		if sprint.StartDate.After(now) {
			continue
		}
		if sprint.EndDate.After(now) {
			continue
		}
		diff := now.Sub(*sprint.EndDate)
		if diff < minDiff {
			minSprint = &sprints[idx]
		}
	}

	return minSprint
}

func getNearestFutureSprint(sprints []jira.Sprint) *jira.Sprint {
	now := time.Now()
	minDiff := time.Hour * 7 * 24
	var minSprint *jira.Sprint
	for idx, sprint := range sprints {
		if !strings.Contains(sprint.Name, config.Jira.Project) {
			// Only care about current project's sprints.
			continue
		}
		// 1. Sprint End Date > Now
		// 2. Min(Sprint Start Date - Now)
		if sprint.EndDate.Before(now) {
			continue
		}
		diff := (*sprint.StartDate).Sub(now)
		if diff < minDiff {
			minSprint = &sprints[idx]
		}
	}

	return minSprint
}

func createSprint(boardID int, name string, startDate, endDate string) jira.Sprint {
	apiEndpoint := "rest/agile/1.0/sprint"
	sprint := map[string]string{
		"name":          name,
		"startDate":     startDate,
		"endDate":       endDate,
		"originBoardId": strconv.Itoa(boardID),
	}
	req, err := jiraClient.NewRequest("POST", apiEndpoint, sprint)
	perror(err)

	responseSprint := new(jira.Sprint)
	_, err = jiraClient.Do(req, responseSprint)
	perror(err)

	return *responseSprint
}

func createNextSprint(boardID int, startDate time.Time) jira.Sprint {
	// We assuem the sprint starts at 00:00 and ends at 00:00
	// E.g, current sprint time range is 2018-09-28T00:00:00+08:00 2018-10-05T00:00:00+08:00
	// So the next sprint is 2018-10-05T00:00:00+08:00, 2018-10-12T00:00:00+08:00
	// The sprint name is 2018-10-05 - 2018-10-11
	endDate := startDate.Add(sprintDuration)

	name := fmt.Sprintf("%s %s - %s", config.Jira.Project, startDate.Format(dayFormat), endDate.Add(-time.Second).Format(dayFormat))

	sprints := getSprints(boardID, jira.GetAllSprintsOptions{
		State: "future",
	})
	for _, sprint := range sprints {
		if sprint.Name == name {
			return sprint
		}
	}

	return createSprint(boardID, name, startDate.Format(dateFormat), endDate.Format(dateFormat))
}

func deleteSprint(sprintID int) {
	apiEndpoint := "rest/agile/1.0/sprint/" + strconv.Itoa(sprintID)
	req, err := jiraClient.NewRequest("DELETE", apiEndpoint, nil)
	perror(err)

	_, err = jiraClient.Do(req, nil)
	perror(err)
}

func updateSprintTime(sprintID int, startDate, endDate string) jira.Sprint {
	return updateSprint(sprintID, map[string]string{
		"startDate": startDate,
		"endDate":   endDate,
	})
}

func updateSprintState(sprintID int, state string) jira.Sprint {
	return updateSprint(sprintID, map[string]string{
		"state": state,
	})
}

func updateSprint(sprintID int, args map[string]string) jira.Sprint {
	apiEndpoint := "rest/agile/1.0/sprint/" + strconv.Itoa(sprintID)

	req, err := jiraClient.NewRequest("POST", apiEndpoint, args)
	perror(err)

	responseSprint := new(jira.Sprint)
	_, err = jiraClient.Do(req, responseSprint)
	perror(err)

	return *responseSprint
}

// A pagination-aware alternative for SprintService.MoveIssuesToSprint.
//
// https://developer.atlassian.com/cloud/jira/software/rest/#api-rest-agile-1-0-sprint-sprintId-issue-post
func moveIssuesToSprint(sprintID int, issues []jira.Issue) {
	apiEndpoint := fmt.Sprintf("rest/agile/1.0/sprint/%d/issue", sprintID)

	// The maximum number of issues that can be moved in one operation is 50.
	batchMax := 50
	buffer := make([]string, 0, batchMax)
	total := len(issues)
	for idx, ise := range issues {
		buffer = append(buffer, ise.ID)
		if len(buffer) == batchMax || idx+1 == total {
			payload := jira.IssuesWrapper{Issues: buffer}
			req, err := jiraClient.NewRequest("POST", apiEndpoint, payload)
			perror(err)
			_, err = jiraClient.Do(req, nil)
			perror(err)

			// clear buffer
			buffer = buffer[:0]
		}
	}
}

func queryJiraIssues(jql string) []jira.Issue {
	issues, _, err := jiraClient.Issue.Search(jql, &jira.SearchOptions{
		MaxResults: 1000,
	})
	perror(err)
	return issues
}
