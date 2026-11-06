// Package fetch looks up a paper's metadata from an arXiv ID or a DOI and
// builds the item the Zotero Connector would have sent.
package fetch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	arxivNew  = regexp.MustCompile(`(?i)(?:arxiv\.org/(?:abs|pdf)/|arxiv:)?\b(\d{4}\.\d{4,5})(?:v\d+)?(?:\.pdf)?$`)
	arxivOld  = regexp.MustCompile(`(?i)(?:arxiv\.org/(?:abs|pdf)/|arxiv:)?\b([a-z-]+(?:\.[A-Z]{2})?/\d{7})(?:v\d+)?(?:\.pdf)?$`)
	doiFormat = regexp.MustCompile(`(?i)\b(10\.\d{4,9}/\S+)$`)
	jatsTags  = regexp.MustCompile(`</?jats:[^>]*>`)
	// Springer abstracts start with a heading that Crossref runs into the text.
	abstractHeading = regexp.MustCompile(`^(A\s?bstract|Abstract)[\s:.]+`)
	bareOldArXiv    = regexp.MustCompile(`^[a-z-]+(\.[A-Z]{2})?/\d{7}$`)
	spaces          = regexp.MustCompile(`\s+`)
)

// Paper is what an identifier resolves to. Item is in the Connector's format,
// so it goes through the same import path as a save from Chrome.
type Paper struct {
	Title  string
	Item   map[string]any
	PDFURL string
	DOI    string
	ArXiv  string
}

// Client fetches metadata over the network.
type Client struct {
	HTTP     *http.Client
	ArXivAPI string
	Crossref string
	ArXivPDF string
}

// NewClient returns a client for the real services.
func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		ArXivAPI: "https://export.arxiv.org/api/query",
		Crossref: "https://api.crossref.org/works/",
		ArXivPDF: "https://arxiv.org/pdf/",
	}
}

// ParseID recognizes an arXiv ID or URL, or a DOI or doi.org URL.
func ParseID(id string) (arxiv, doi string, err error) {
	id = strings.TrimSpace(strings.TrimSuffix(id, "/"))
	if m := arxivNew.FindStringSubmatch(id); m != nil {
		return m[1], "", nil
	}
	if m := arxivOld.FindStringSubmatch(id); m != nil && strings.Contains(strings.ToLower(id), "arxiv") {
		return m[1], "", nil
	}
	if bareOldArXiv.MatchString(id) {
		return id, "", nil
	}
	if unescaped, err := url.PathUnescape(id); err == nil {
		id = unescaped
	}
	if m := doiFormat.FindStringSubmatch(id); m != nil {
		return "", strings.TrimRight(m[1], ".,;"), nil
	}
	return "", "", fmt.Errorf("%q isn't an arXiv ID or a DOI", id)
}

// Lookup resolves an identifier.
func (c *Client) Lookup(id string) (Paper, error) {
	arxiv, doi, err := ParseID(id)
	if err != nil {
		return Paper{}, err
	}
	if arxiv != "" {
		return c.arXiv(arxiv)
	}
	return c.crossref(doi)
}

type atomFeed struct {
	Entries []struct {
		ID        string `xml:"id"`
		Title     string `xml:"title"`
		Summary   string `xml:"summary"`
		Published string `xml:"published"`
		DOI       string `xml:"http://arxiv.org/schemas/atom doi"`
		Journal   string `xml:"http://arxiv.org/schemas/atom journal_ref"`
		Primary   struct {
			Term string `xml:"term,attr"`
		} `xml:"http://arxiv.org/schemas/atom primary_category"`
		Authors []struct {
			Name string `xml:"name"`
		} `xml:"author"`
	} `xml:"entry"`
}

func (c *Client) arXiv(id string) (Paper, error) {
	body, err := c.get(c.ArXivAPI + "?id_list=" + url.QueryEscape(id))
	if err != nil {
		return Paper{}, err
	}
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return Paper{}, fmt.Errorf("read arXiv's answer: %w", err)
	}
	if len(feed.Entries) == 0 || feed.Entries[0].Title == "" || feed.Entries[0].Title == "Error" {
		return Paper{}, fmt.Errorf("arXiv has no paper %s", id)
	}
	e := feed.Entries[0]
	var creators []map[string]string
	for _, a := range e.Authors {
		creators = append(creators, splitName(a.Name))
	}
	item := map[string]any{
		"itemType":       "preprint",
		"title":          clean(e.Title),
		"creators":       creators,
		"abstractNote":   clean(e.Summary),
		"date":           firstN(e.Published, 10),
		"url":            "https://arxiv.org/abs/" + id,
		"repository":     "arXiv",
		"archiveID":      "arXiv:" + id,
		"DOI":            "10.48550/arXiv." + id,
		"libraryCatalog": "arXiv.org",
		"accessDate":     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	extra := []string{}
	if e.Primary.Term != "" {
		item["number"] = "arXiv:" + id
		extra = append(extra, "arXiv:"+id+" ["+e.Primary.Term+"]")
	}
	if e.DOI != "" {
		extra = append(extra, "Published DOI: "+e.DOI)
	}
	if e.Journal != "" {
		extra = append(extra, "Journal reference: "+clean(e.Journal))
	}
	if len(extra) > 0 {
		item["extra"] = strings.Join(extra, "\n")
	}
	return Paper{Title: clean(e.Title), Item: item, PDFURL: c.ArXivPDF + id, ArXiv: id, DOI: e.DOI}, nil
}

var crossrefTypes = map[string]string{
	"journal-article":     "journalArticle",
	"proceedings-article": "conferencePaper",
	"book-chapter":        "bookSection",
	"book":                "book",
	"posted-content":      "preprint",
	"dissertation":        "thesis",
	"report":              "report",
}

func (c *Client) crossref(doi string) (Paper, error) {
	body, err := c.get(c.Crossref + url.PathEscape(doi))
	if err != nil {
		return Paper{}, err
	}
	var resp struct {
		Message struct {
			Type      string   `json:"type"`
			Title     []string `json:"title"`
			Container []string `json:"container-title"`
			Short     []string `json:"short-container-title"`
			Volume    string   `json:"volume"`
			Issue     string   `json:"issue"`
			Page      string   `json:"page"`
			Article   string   `json:"article-number"`
			Abstract  string   `json:"abstract"`
			Publisher string   `json:"publisher"`
			ISSN      []string `json:"ISSN"`
			URL       string   `json:"URL"`
			Issued    struct {
				DateParts [][]int `json:"date-parts"`
			} `json:"issued"`
			Author []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"author"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Paper{}, fmt.Errorf("read Crossref's answer: %w", err)
	}
	w := resp.Message
	if len(w.Title) == 0 {
		return Paper{}, fmt.Errorf("Crossref has no title for %s", doi)
	}
	itemType := crossrefTypes[w.Type]
	if itemType == "" {
		itemType = "journalArticle"
	}
	var creators []map[string]string
	for _, a := range w.Author {
		if a.Family == "" {
			creators = append(creators, map[string]string{"lastName": a.Name, "creatorType": "author", "fieldMode": "1"})
			continue
		}
		creators = append(creators, map[string]string{"firstName": a.Given, "lastName": a.Family, "creatorType": "author"})
	}
	pages := w.Page
	if pages == "" {
		pages = w.Article
	}
	item := map[string]any{
		"itemType":       itemType,
		"title":          clean(w.Title[0]),
		"creators":       creators,
		"DOI":            doi,
		"url":            w.URL,
		"volume":         w.Volume,
		"issue":          w.Issue,
		"pages":          pages,
		"publisher":      w.Publisher,
		"date":           dateFromParts(w.Issued.DateParts),
		"abstractNote":   abstractHeading.ReplaceAllString(clean(jatsTags.ReplaceAllString(w.Abstract, " ")), ""),
		"libraryCatalog": "DOI.org (Crossref)",
		"accessDate":     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if len(w.Container) > 0 {
		venue := map[string]string{"conferencePaper": "proceedingsTitle", "bookSection": "bookTitle"}[itemType]
		if venue == "" {
			venue = "publicationTitle"
		}
		item[venue] = clean(w.Container[0])
	}
	if len(w.Short) > 0 && itemType == "journalArticle" {
		item["journalAbbreviation"] = w.Short[0]
	}
	if len(w.ISSN) > 0 && itemType == "journalArticle" {
		item["ISSN"] = strings.Join(w.ISSN, ", ")
	}
	for k, v := range item {
		if s, ok := v.(string); ok && s == "" {
			delete(item, k)
		}
	}
	return Paper{Title: clean(w.Title[0]), Item: item, DOI: doi}, nil
}

// DownloadPDF fetches a PDF and checks that it is one, since a paywall or
// error page often comes back with a 200.
func (c *Client) DownloadPDF(pdfURL string) ([]byte, error) {
	body, err := c.get(pdfURL)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(body[:min(len(body), 1024)], []byte("%PDF-")) {
		return nil, errors.New("the download isn't a PDF")
	}
	return body, nil
}

func (c *Client) get(target string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Crossref asks clients to identify themselves.
	req.Header.Set("User-Agent", "zotty (+https://github.com/xin-yi6/zotty)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s has no record of that", req.URL.Host)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", req.URL.Host, resp.StatusCode)
	}
	return io.ReadAll(bufio.NewReader(io.LimitReader(resp.Body, 200<<20)))
}

// splitName turns "Jean-Luc Picard" into first and last names, last word last.
// A group such as "The CMS Collaboration" stays one name.
func splitName(name string) map[string]string {
	name = clean(name)
	i := strings.LastIndex(name, " ")
	if i < 0 || groupName.MatchString(name) {
		return map[string]string{"lastName": name, "creatorType": "author", "fieldMode": "1"}
	}
	return map[string]string{"firstName": name[:i], "lastName": name[i+1:], "creatorType": "author"}
}

var groupName = regexp.MustCompile(`(?i)\b(collaboration|consortium|group|team|project)\b`)

func clean(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(s), " "))
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func dateFromParts(parts [][]int) string {
	if len(parts) == 0 || len(parts[0]) == 0 {
		return ""
	}
	var out []string
	for i, p := range parts[0] {
		if i == 0 {
			out = append(out, strconv.Itoa(p))
		} else {
			out = append(out, fmt.Sprintf("%02d", p))
		}
	}
	return strings.Join(out, "-")
}
