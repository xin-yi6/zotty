package fetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	cases := map[string][2]string{
		"2610.40775":                                {"2610.40775", ""},
		"https://arxiv.org/abs/2610.40775v2":        {"2610.40775", ""},
		"https://arxiv.org/pdf/2610.40775v1.pdf":    {"2610.40775", ""},
		"arXiv:2610.40775":                          {"2610.40775", ""},
		"arXiv:hep-ph/0505265":                      {"hep-ph/0505265", ""},
		"hep-ph/0505265":                            {"hep-ph/0505265", ""},
		"https://arxiv.org/abs/hep-th/9711200":      {"hep-th/9711200", ""},
		"10.1007/JHEP06(2026)030":                   {"", "10.1007/JHEP06(2026)030"},
		"https://doi.org/10.1103/rn6v-jwbz":         {"", "10.1103/rn6v-jwbz"},
		"https://doi.org/10.1007%2FJHEP06(2026)030": {"", "10.1007/JHEP06(2026)030"},
	}
	for in, want := range cases {
		arxiv, doi, err := ParseID(in)
		if err != nil || arxiv != want[0] || doi != want[1] {
			t.Errorf("%s: got %q %q %v", in, arxiv, doi, err)
		}
	}
	if _, _, err := ParseID("holographic QCD"); err == nil {
		t.Error("accepted a phrase")
	}
}

const arxivAnswer = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <id>http://arxiv.org/abs/2610.40775v1</id>
    <published>2026-10-30T17:59:59Z</published>
    <title>On the Role of Gravity in
      Supercooled Phase Transitions</title>
    <summary>  We study &amp; compute.  </summary>
    <author><name>Alberto Cutrona</name></author>
    <author><name>Alberto Salvio</name></author>
    <arxiv:primary_category term="hep-ph"/>
  </entry>
</feed>`

func TestArXiv(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id_list") != "2610.40775" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(arxivAnswer))
	}))
	defer ts.Close()
	c := &Client{HTTP: ts.Client(), ArXivAPI: ts.URL, ArXivPDF: "https://arxiv.org/pdf/"}
	p, err := c.Lookup("https://arxiv.org/abs/2610.40775v1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "On the Role of Gravity in Supercooled Phase Transitions" {
		t.Errorf("title = %q", p.Title)
	}
	if p.PDFURL != "https://arxiv.org/pdf/2610.40775" || p.Item["archiveID"] != "arXiv:2610.40775" {
		t.Errorf("paper = %+v", p)
	}
	creators, _ := json.Marshal(p.Item["creators"])
	if !strings.Contains(string(creators), `"firstName":"Alberto","lastName":"Cutrona"`) {
		t.Errorf("creators = %s", creators)
	}
	if p.Item["abstractNote"] != "We study & compute." {
		t.Errorf("abstract = %q", p.Item["abstractNote"])
	}
}

func TestCrossref(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"type":"journal-article","title":["Phase structure"],
			"container-title":["Journal of High Energy Physics"],"volume":"2026","issue":"6","article-number":"30",
			"issued":{"date-parts":[[2026,6,3]]},"abstract":"<jats:title>A bstract</jats:title><jats:p>We construct.</jats:p>",
			"author":[{"given":"William","family":"Riker"},{"name":"ATLAS Collaboration"}]}}`))
	}))
	defer ts.Close()
	c := &Client{HTTP: ts.Client(), Crossref: ts.URL + "/"}
	p, err := c.Lookup("10.1007/JHEP06(2026)030")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"itemType": "journalArticle", "publicationTitle": "Journal of High Energy Physics",
		"pages": "30", "date": "2026-06-03", "abstractNote": "We construct.", "DOI": "10.1007/JHEP06(2026)030",
	}
	for k, v := range want {
		if p.Item[k] != v {
			t.Errorf("%s = %v, want %v", k, p.Item[k], v)
		}
	}
	if p.PDFURL != "" {
		t.Error("a DOI should not promise a PDF")
	}
}

func TestDownloadRejectsNonPDF(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>sign in</html>"))
	}))
	defer ts.Close()
	c := &Client{HTTP: ts.Client()}
	if _, err := c.DownloadPDF(ts.URL); err == nil {
		t.Error("accepted HTML as a PDF")
	}
}

func TestSplitName(t *testing.T) {
	for name, want := range map[string]string{
		"Jean-Luc Picard":         "Jean-Luc|Picard",
		"Plato":                   "|Plato",
		"CMS Collaboration":       "|CMS Collaboration",
		"The ATLAS collaboration": "|The ATLAS collaboration",
		"LIGO Scientific Collaboration and Virgo Collaboration": "|LIGO Scientific Collaboration and Virgo Collaboration",
	} {
		got := splitName(name)
		if got["firstName"]+"|"+got["lastName"] != want {
			t.Errorf("%s: got %q %q, want %s", name, got["firstName"], got["lastName"], want)
		}
	}
}
