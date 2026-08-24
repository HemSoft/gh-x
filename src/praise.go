package main

import (
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
)

var backlogPraises = []string{
	"✅ Backlog cleared!",
	"✅ Good job, here is a pony!",
	"✅ Inbox zero, engineering edition.",
	"✅ Queue conquered.",
	"✅ Nothing waiting. Take the win.",
	"✅ The backlog goblin has been vanquished.",
	"✅ All clear. Future you says thanks.",
	"✅ No open work. Victory lap approved.",
	"✅ The board is spotless. Suspiciously spotless.",
	"✅ Shipshape and backlog-free.",
	"✅ You defeated the queue.",
	"✅ The backlog has left the building.",
	"✅ Everything is handled. Go make something weird.",
	"✅ Clean slate achieved.",
	"✅ The queue is empty. Coffee tastes better now.",
}

var backlogPraiseIndex = rand.IntN

func writeBacklogPraise(stdout io.Writer) {
	fmt.Fprintln(stdout, backlogPraiseAt(backlogPraiseIndex(len(backlogPraises))))
}

func backlogPraiseAt(index int) string {
	if index < 0 || index >= len(backlogPraises) {
		return backlogPraises[0]
	}
	return backlogPraises[index]
}

func isOpenPullRequestBacklog(options listOptions) bool {
	return strings.EqualFold(options.state, "open") &&
		options.author == "" && options.assignee == "" && options.app == "" &&
		options.base == "" && options.head == "" && options.search == "" &&
		!options.draftOnly && len(options.labels) == 0
}

func isOpenIssueBacklog(options issueListOptions) bool {
	return strings.EqualFold(options.state, "open") &&
		options.author == "" && options.assignee == "" && options.milestone == "" &&
		options.search == "" && len(options.labels) == 0
}
