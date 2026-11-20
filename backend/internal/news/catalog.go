package news

import (
	"html"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"smmagent/internal/model"
)

// CatalogSignature describes a report for the shadow catalog. Reading is populated
// only when a legislative reading is explicitly stated near the start.
type CatalogSignature struct {
	URL      string   `json:"url"`
	Terms    []string `json:"terms"`
	Place    string   `json:"place,omitempty"`
	Document string   `json:"document,omitempty"`
	Reading  int      `json:"reading,omitempty"`
}

type CatalogVersion struct {
	EventID   int64
	Version   int
	Signature CatalogSignature
}

var (
	readingPattern  = regexp.MustCompile(`(?:^| )((?:перв|втор|трет)[а-я]+|окончательн[а-я]+|i{1,3}|[123](?: м)?) (?:окончательн[а-я]+ )?чтени[а-я]*(?: |$)`)
	billPattern     = regexp.MustCompile(`(?i)законопроект[а-я]*[ \x{00a0}]+№[ \x{00a0}]*([0-9]+-[0-9]+)`)
	adoptionPattern = regexp.MustCompile(`(?:^| )((?:не )?(?:принял|принят|одобрил|одобрен|поддержал|поддержан)[а-я]*)(?: |$)`)
)

// A small list of recognised jurisdictions supports conservative matches between
// differently worded legislative headlines. Unknown jurisdictions use title matching.
var catalogPlaces = []struct {
	name     string
	prefixes []string
}{
	{"saint-petersburg", []string{"петербург", "санкт петербург", "питер"}},
	{"leningrad-region", []string{"ленобласт", "ленинградск"}},
	{"moscow", []string{"москв", "московск"}},
	{"estonia", []string{"эстон"}},
	{"germany", []string{"герман", "фрг", "немецк"}},
}

func DescribeCatalogMaterial(material model.Material, lexemes []string) CatalogSignature {
	title := normalize(html.UnescapeString(material.Title))
	body := []rune(html.UnescapeString(material.Body))
	lead := normalize(string(body[:min(len(body), 420)]))
	text := title + " " + lead
	signature := CatalogSignature{URL: material.URL, Place: catalogPlace(title)}
	if signature.Place == "" {
		signature.Place = catalogPlace(lead)
	}
	if match := billPattern.FindStringSubmatch(material.Title + " " + string(body[:min(len(body), 420)])); match != nil {
		signature.Document = match[1]
	}
	adoption := adoptionPattern.FindStringSubmatch(text)
	if match := readingPattern.FindStringSubmatch(text); match != nil && adoption != nil && !strings.HasPrefix(adoption[1], "не ") {
		ordinal := match[1]
		switch {
		case strings.HasPrefix(ordinal, "перв"), ordinal == "i", strings.HasPrefix(ordinal, "1"):
			signature.Reading = 1
		case strings.HasPrefix(ordinal, "втор"), ordinal == "ii", strings.HasPrefix(ordinal, "2"):
			signature.Reading = 2
		default:
			signature.Reading = 3
		}
	}
	for _, term := range lexemes {
		if len([]rune(term)) >= 3 && strings.IndexFunc(term, unicode.IsLetter) >= 0 && !stopWords[term] {
			signature.Terms = append(signature.Terms, term)
		}
	}
	return signature
}

func catalogPlace(text string) string {
	// In a lead containing multiple locations, prefer the first mention.
	best, place := len(text)+1, ""
	for _, candidate := range catalogPlaces {
		for _, prefix := range candidate.prefixes {
			if pos := strings.Index(" "+text, " "+prefix); pos >= 0 && pos < best {
				best, place = pos, candidate.name
			}
		}
	}
	return place
}

// MatchCatalogEvent compares with the version currently representing each event.
// An equally strong match to two events is too ambiguous to attach automatically.
func MatchCatalogEvent(incoming CatalogSignature, candidates []CatalogVersion) CatalogVersion {
	best, bestScore, tied := CatalogVersion{}, 0, false
	for _, candidate := range candidates {
		score := catalogSimilarity(incoming, candidate.Signature)
		if score > bestScore {
			best, bestScore, tied = candidate, score, false
		} else if score > 0 && score == bestScore && candidate.EventID != best.EventID {
			tied = true
		}
	}
	if tied {
		return CatalogVersion{}
	}
	return best
}

func catalogSimilarity(a, b CatalogSignature) int {
	if a.Document != "" && b.Document != "" {
		if a.Document == b.Document {
			return 2000
		}
		return 0
	}
	if a.Place != "" && b.Place != "" && a.Place != b.Place {
		return 0
	}
	if a.URL != "" && a.URL == b.URL {
		return 1000
	}
	common := 0
	for _, term := range a.Terms {
		if slices.Contains(b.Terms, term) {
			common++
		}
	}
	union := len(a.Terms) + len(b.Terms) - common
	if common >= 5 && 2*common >= union {
		return common * 100 / union
	}
	if a.Reading > 0 && b.Reading > 0 && a.Place != "" && a.Place == b.Place {
		// Compare the subject without legislative boilerplate or the jurisdiction name.
		left, right := catalogSubject(a.Terms), catalogSubject(b.Terms)
		shared := 0
		for _, term := range left {
			if slices.Contains(right, term) {
				shared++
			}
		}
		if shared >= 3 && 2*shared >= min(len(left), len(right)) {
			return shared * 100 / (len(left) + len(right) - shared)
		}
	}
	return 0
}

func catalogSubject(terms []string) []string {
	var result []string
	for _, term := range terms {
		switch term {
		case "закон", "законопроект", "чтен", "депутат", "приня", "одобр", "закс", "парламент",
			"перв", "втор", "трет", "окончательн", "сми", "пишут", "год", "лет":
			continue
		}
		if catalogPlace(term) == "" {
			result = append(result, term)
		}
	}
	return result
}
