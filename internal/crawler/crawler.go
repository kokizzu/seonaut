package crawler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/mnlg/lenkrr/internal/report"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/queue"
	"github.com/microcosm-cc/bluemonday"
)

type Crawler struct {
	URL             *url.URL
	MaxPageReports  int
	IgnoreRobotsTxt bool
	UserAgent       string
	sanitizer       *bluemonday.Policy
}

func (c *Crawler) Crawl(pr chan<- report.PageReport) {
	defer close(pr)

	q, _ := queue.New(
		consumerThreads,
		&queue.InMemoryQueueStorage{MaxSize: storageMaxSize},
	)

	var responseCounter int
	cor := colly.NewCollector(
		colly.UserAgent(c.UserAgent),
		func(co *colly.Collector) {
			co.IgnoreRobotsTxt = c.IgnoreRobotsTxt
		},
	)

	handleResourceResponse := func(r *colly.Response) {
		if responseCounter >= c.MaxPageReports {
			return
		}
		url := r.Request.URL
		pageReport := report.NewPageReport(url, r.StatusCode, r.Headers, r.Body, c.sanitizer)
		pr <- *pageReport
		responseCounter++
	}

	handleResponse := func(r *colly.Response) {
		if responseCounter >= c.MaxPageReports {
			return
		}

		url := r.Request.URL
		pageReport := report.NewPageReport(url, r.StatusCode, r.Headers, r.Body, c.sanitizer)

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

			q.AddURL(r.Request.AbsoluteURL(l.URL))
		}

		if pageReport.RedirectURL != "" {
			q.AddURL(r.Request.AbsoluteURL(pageReport.RedirectURL))
		}

		for _, l := range pageReport.Hreflangs {
			q.AddURL(r.Request.AbsoluteURL(l.URL))
		}

		if pageReport.Canonical != "" {
			q.AddURL(r.Request.AbsoluteURL(pageReport.Canonical))
		}

		var resources []string

		for _, l := range pageReport.Scripts {
			resources = append(resources, r.Request.AbsoluteURL(l))
		}

		for _, l := range pageReport.Styles {
			resources = append(resources, r.Request.AbsoluteURL(l))
		}

		for _, l := range pageReport.Images {
			resources = append(resources, r.Request.AbsoluteURL(l.URL))
		}

		if len(resources) > 0 {
			qr, _ := queue.New(
				consumerThreads,
				&queue.InMemoryQueueStorage{MaxSize: storageMaxSize},
			)

			for _, v := range resources {
				qr.AddURL(v)
			}

			qr.Run(cor)
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

	cor.OnResponse(handleResourceResponse)

	cor.OnError(func(r *colly.Response, err error) {
		if r.StatusCode > 0 && r.Headers != nil {
			handleResourceResponse(r)
		}
	})

	cor.SetRedirectHandler(func(r *http.Request, via []*http.Request) error {
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

	q.AddURL(us)
	q.Run(co)
}
