package main

import (
	"regexp"
	"strconv"
	"strings"
)

var columns = regexp.MustCompile(`\s{2,}|\t`)

type Server struct {
	Index    string `json:"index"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Tag      string `json:"tag"`
}

func parseServers(output string) []Server {
	servers := []Server{}
	for _, line := range strings.Split(output, "\n") {
		fields := columns.Split(strings.TrimSpace(line), 4)
		if len(fields) < 3 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		server := Server{Index: fields[0], Protocol: fields[1], Address: fields[2]}
		if len(fields) == 4 {
			server.Tag = fields[3]
		}
		servers = append(servers, server)
	}
	return servers
}

type Subscription struct {
	Active  bool   `json:"active"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Servers string `json:"servers"`
	Details string `json:"details"`
}

func parseSubscriptions(output string) []Subscription {
	subscriptions := []Subscription{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		marked, active := strings.CutPrefix(line, "*")
		fields := columns.Split(strings.TrimSpace(marked), -1)
		if len(fields) < 2 || (!active && fields[0] == "ACTIVE") {
			continue
		}

		subscription := Subscription{Active: active, Name: fields[0], Title: fields[1]}
		if len(fields) > 2 {
			subscription.Servers = fields[2]
			subscription.Details = strings.Join(fields[3:], " ")
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions
}
