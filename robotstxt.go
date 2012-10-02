// The robots.txt Exclusion Protocol is implemented as specified in
// http://www.robotstxt.org/wc/robots.html
// with various extensions.
package robotstxt

// Comments explaining the logic are taken from either the google's spec:
// https://developers.google.com/webmasters/control-crawl-index/docs/robots_txt

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type RobotsData struct {
	// private
	groups      []*Group
	allowAll    bool
	disallowAll bool
	Sitemaps    []string
}

type Group struct {
	agents     []string
	rules      []*rule
	CrawlDelay time.Duration
}

type rule struct {
	path    string
	allow   bool
	pattern *regexp.Regexp
}

var allowAll = &RobotsData{allowAll: true}
var disallowAll = &RobotsData{disallowAll: true}

func FromResponseBytes(statusCode int, body []byte) (*RobotsData, error) {
	switch {
	//
	// From https://developers.google.com/webmasters/control-crawl-index/docs/robots_txt
	//
	// Google treats all 4xx errors in the same way and assumes that no valid
	// robots.txt file exists. It is assumed that there are no restrictions.
	// This is a "full allow" for crawling. Note: this includes 401
	// "Unauthorized" and 403 "Forbidden" HTTP result codes.
	case statusCode >= 400 && statusCode < 500:
		return allowAll, nil
	case statusCode >= 200 && statusCode < 300:
		return FromBytes(body)
	}
	// Conservative disallow all default
	//
	// From https://developers.google.com/webmasters/control-crawl-index/docs/robots_txt
	//
	// Server errors (5xx) are seen as temporary errors that result in a "full
	// disallow" of crawling.
	return disallowAll, nil
}

func FromResponseContent(statusCode int, body string) (*RobotsData, error) {
	return FromResponseBytes(statusCode, []byte(body))
}

func FromResponse(res *http.Response) (*RobotsData, error) {
	if res == nil {
		// Edge case, if res is nil, return nil data
		return nil, nil
	}
	buf, e := ioutil.ReadAll(res.Body)
	if e != nil {
		return nil, e
	}
	return FromResponseBytes(res.StatusCode, buf)
}

func FromBytes(body []byte) (r *RobotsData, err error) {
	var errs []error

	// special case (probably not worth optimization?)
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return allowAll, nil
	}

	sc := newByteScanner("bytes", false)
	//sc.Quiet = !print_errors
	sc.Feed(body, true)
	var tokens []string
	tokens, err = sc.ScanAll()
	if err != nil {
		return nil, err
	}

	// special case worth optimization
	if len(tokens) == 0 {
		return allowAll, nil
	}

	r = &RobotsData{}
	parser := newParser(tokens)
	r.groups, r.Sitemaps, errs = parser.parseAll()
	if len(errs) > 0 {
		// TODO : Return error messages as a slice of messages on the error?
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return nil, errors.New("Parse error. Use print_errors = true to print on stderr.")
	}

	return r, nil
}

func FromString(body string) (r *RobotsData, err error) {
	return FromBytes([]byte(body))
}

func (r *RobotsData) TestAgent(path, agent string) bool {
	if r.allowAll {
		return true
	}
	if r.disallowAll {
		return false
	}

	// Find a group of rules that applies to this agent
	// From google's spec:
	// The user-agent is non-case-sensitive.
	if g := r.FindGroup(agent); g != nil {
		// Find a rule that applies to this url
		if r := g.findRule(path); r != nil {
			return r.allow
		}
	}

	// From google's spec:
	// By default, there are no restrictions for crawling for the designated crawlers. 
	return true
}

// From google's spec:
// Only one group of group-member records is valid for a particular crawler.
// The crawler must determine the correct group of records by finding the group
// with the most specific user-agent that still matches. All other groups of
// records are ignored by the crawler. The user-agent is non-case-sensitive.
// The order of the groups within the robots.txt file is irrelevant.
func (r *RobotsData) FindGroup(agent string) (ret *Group) {
	var prefixLen int

	agent = strings.ToLower(agent)
	for _, g := range r.groups {
		for _, a := range g.agents {
			if a == "*" && prefixLen == 0 {
				// Weakest match possible
				prefixLen = 1
				ret = g
			} else if strings.HasPrefix(agent, a) {
				if l := len(a); l > prefixLen {
					prefixLen = l
					ret = g
				}
			}
		}
	}
	return
}

func (g *Group) Test(path string) bool {
	if r := g.findRule(path); r != nil {
		return r.allow
	}

	return true
}

// From google's spec:
// The path value is used as a basis to determine whether or not a rule applies
// to a specific URL on a site. With the exception of wildcards, the path is
// used to match the beginning of a URL (and any valid URLs that start with the
// same path).
//
// At a group-member level, in particular for allow and disallow directives,
// the most specific rule based on the length of the [path] entry will trump
// the less specific (shorter) rule. The order of precedence for rules with
// wildcards is undefined.
func (g *Group) findRule(path string) (ret *rule) {
	var prefixLen int

	for _, r := range g.rules {
		if r.pattern != nil {
			if r.pattern.MatchString(path) {
				// Consider this a match equal to the length of the pattern.
				// From google's spec:
				// The order of precedence for rules with wildcards is undefined.
				if l := len(r.pattern.String()); l > prefixLen {
					prefixLen = len(r.pattern.String())
					ret = r
				}
			}
		} else if r.path == "/" && prefixLen == 0 {
			// Weakest match possible
			prefixLen = 1
			ret = r
		} else if strings.HasPrefix(path, r.path) {
			if l := len(r.path); l > prefixLen {
				prefixLen = l
				ret = r
			}
		}
	}
	return
}
