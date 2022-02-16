package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/queue"
)

const (
	consumerThreads        = 2
	storageMaxSize         = 10000
	MaxPageReports         = 500
	AdvancedMaxPageReports = 5000
	RendertronURL          = "http://127.0.0.1:3000/render/"
)

type Crawler struct {
	URL             *url.URL
	MaxPageReports  int
	UseJS           bool
	IgnoreRobotsTxt bool
	UserAgent       string
}

func startCrawler(p Project, agent string, advanced bool, datastore *datastore) int {
	var totalURLs int
	var max int

	if advanced {
		max = AdvancedMaxPageReports
	} else {
		max = MaxPageReports
	}

	u, err := url.Parse(p.URL)
	if err != nil {
		fmt.Println(err)
		return 0
	}

	c := &Crawler{
		URL:             u,
		MaxPageReports:  max,
		UseJS:           p.UseJS,
		IgnoreRobotsTxt: p.IgnoreRobotsTxt,
		UserAgent:       agent,
	}

	cid := datastore.saveCrawl(p)

	pageReport := make(chan PageReport)
	go c.Crawl(pageReport)

	for r := range pageReport {
		totalURLs++
		datastore.savePageReport(&r, cid)
	}

	datastore.saveEndCrawl(cid, time.Now(), totalURLs)
	fmt.Printf("%d pages crawled.\n", totalURLs)

	return int(cid)
}

func (c *Crawler) Crawl(pr chan<- PageReport) {
	defer close(pr)

	q, _ := queue.New(
		consumerThreads,
		&queue.InMemoryQueueStorage{MaxSize: storageMaxSize},
	)

	var responseCounter int

	handleResponse := func(r *colly.Response) {
		if responseCounter >= c.MaxPageReports {
			return
		}

		us := r.Request.URL.String()
		url := r.Request.URL
		if c.UseJS == true {
			us = us[len(RendertronURL):]
			url, _ = url.Parse(us)
		}
		pageReport := NewPageReport(url, r.StatusCode, r.Headers, r.Body)

		if strings.Contains(pageReport.Robots, "noindex") {
			return
		}

		pr <- *pageReport
		responseCounter++

		if strings.Contains(pageReport.Robots, "nofollow") {
			return
		}

		for _, l := range pageReport.Links {
			if l.NoFollow {
				continue
			}

			lurl := r.Request.AbsoluteURL(l.URL)
			if c.UseJS == true {
				lurl = RendertronURL + lurl
			}

			q.AddURL(lurl)
		}

		if pageReport.RedirectURL != "" {
			q.AddURL(r.Request.AbsoluteURL(pageReport.RedirectURL))
		}

		for _, l := range pageReport.Scripts {
			q.AddURL(r.Request.AbsoluteURL(l))
		}

		for _, l := range pageReport.Styles {
			q.AddURL(r.Request.AbsoluteURL(l))
		}

		for _, l := range pageReport.Images {
			q.AddURL(r.Request.AbsoluteURL(l.URL))
		}

		for _, l := range pageReport.Hreflangs {
			q.AddURL(r.Request.AbsoluteURL(l.URL))
		}

		if pageReport.Canonical != "" {
			q.AddURL(r.Request.AbsoluteURL(pageReport.Canonical))
		}
	}

	var nonWWWHost string
	var WWWHost string
	if strings.HasPrefix(c.URL.Host, "www.") {
		WWWHost = c.URL.Host
		nonWWWHost = c.URL.Host[4:]
	} else {
		WWWHost = "www." + c.URL.Host
		nonWWWHost = c.URL.Host
	}

	co := colly.NewCollector(
		colly.AllowedDomains(WWWHost, nonWWWHost, "127.0.0.1"),
		colly.UserAgent(c.UserAgent),
		func(co *colly.Collector) {
			co.IgnoreRobotsTxt = c.IgnoreRobotsTxt
		},
	)

	co.OnRequest(func(r *colly.Request) {
		// fmt.Printf("Visiting %s\n", r.URL.String())
	})

	co.OnResponse(handleResponse)

	co.OnError(func(r *colly.Response, err error) {
		if r.StatusCode > 0 && r.Headers != nil {
			handleResponse(r)
		}
	})

	co.SetRedirectHandler(func(r *http.Request, via []*http.Request) error {
		for _, v := range via {
			if v.URL.Path == "/robots.txt" {
				return nil
			}
		}

		return http.ErrUseLastResponse
	})

	if c.URL.Path == "" {
		c.URL.Path = "/"
	}

	us := c.URL.String()
	if c.UseJS == true {
		us = RendertronURL + us
		n, _ := time.ParseDuration("30s")
		co.SetRequestTimeout(n)
	}

	q.AddURL(us)

	q.Run(co)
}
