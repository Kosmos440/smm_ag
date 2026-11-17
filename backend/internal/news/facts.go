package news

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"smmagent/internal/model"
)

const numberPattern = `[0-9]+(?:[ \x{00a0}][0-9]{3})*(?:[.,][0-9]+)?`

var (
	documentPattern = regexp.MustCompile(`(?i)(?:№|\bno[.]?)\s*([0-9]+(?:[-/][a-zа-я0-9]+)*)`)
	isoDatePattern  = regexp.MustCompile(`\b(20[0-9]{2})-([0-9]{2})-([0-9]{2})\b`)
	dmyDatePattern  = regexp.MustCompile(`\b([0-9]{1,2})[./]([0-9]{1,2})[./](20[0-9]{2})\b`)
	wordDatePattern = regexp.MustCompile(`(?i)\b([0-9]{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)(?:\s+(20[0-9]{2}))?`)
	moneyPattern    = regexp.MustCompile(`(?i)(` + numberPattern + `)\s*(млрд|млн|тыс[.]?)?\s*(₽|рублей|рубля|рубль|руб[.]?|usd|долларов|доллара|доллар|eur|евро)(?:$|[^\pL\pN])`)
	percentPattern  = regexp.MustCompile(`(?i)(` + numberPattern + `)\s*(%|процентов|процента|процент)(?:$|[^\pL\pN])`)
	durationPattern = regexp.MustCompile(`(?i)(` + numberPattern + `)\s*(календарных |рабочих )?(дней|дня|день|недель|недели|неделю|месяцев|месяца|месяц)(?:$|[^\pL\pN])`)
)

var months = map[string]string{
	"января": "01", "февраля": "02", "марта": "03", "апреля": "04",
	"мая": "05", "июня": "06", "июля": "07", "августа": "08",
	"сентября": "09", "октября": "10", "ноября": "11", "декабря": "12",
}

func extractFacts(text, sourceURL, organization string) []model.Fact {
	var facts []model.Fact
	seen := make(map[string]bool)
	add := func(kind, value string) {
		key := kind + ":" + value
		if len(facts) >= 32 || value == "" || seen[key] {
			return
		}
		seen[key] = true
		facts = append(facts, model.Fact{Key: kind, Value: value, URL: sourceURL, Organization: organization})
	}
	for _, match := range documentPattern.FindAllStringSubmatch(text, 32) {
		add("document", strings.ToLower(match[1]))
	}
	for _, match := range isoDatePattern.FindAllStringSubmatch(text, 32) {
		add("date", dateValue(match[1], match[2], match[3]))
	}
	for _, match := range dmyDatePattern.FindAllStringSubmatch(text, 32) {
		add("date", dateValue(match[3], match[2], match[1]))
	}
	for _, match := range wordDatePattern.FindAllStringSubmatch(text, 32) {
		add("date", dateValue(match[3], months[strings.ToLower(match[2])], match[1]))
	}
	for _, match := range moneyPattern.FindAllStringSubmatch(text, 32) {
		number := normalizeNumber(match[1])
		scale := strings.TrimSuffix(strings.ToLower(match[2]), ".")
		if scale != "" {
			number += " " + scale
		}
		currency := strings.ToLower(match[3])
		switch {
		case currency == "₽" || strings.HasPrefix(currency, "руб"):
			currency = "RUB"
		case currency == "usd" || strings.HasPrefix(currency, "доллар"):
			currency = "USD"
		case currency == "eur" || currency == "евро":
			currency = "EUR"
		}
		add("money", number+" "+currency)
	}
	for _, match := range percentPattern.FindAllStringSubmatch(text, 32) {
		add("percent", normalizeNumber(match[1])+"%")
	}
	for _, match := range durationPattern.FindAllStringSubmatch(text, 32) {
		unit := "дн."
		switch {
		case strings.HasPrefix(strings.ToLower(match[3]), "нед"):
			unit = "нед."
		case strings.HasPrefix(strings.ToLower(match[3]), "мес"):
			unit = "мес."
		}
		add("duration", normalizeNumber(match[1])+" "+strings.ToLower(match[2])+unit)
	}
	return facts
}

func dateValue(year, month, day string) string {
	if len(month) == 1 {
		month = "0" + month
	}
	if len(day) == 1 {
		day = "0" + day
	}
	date := year + "-" + month + "-" + day
	validation := date
	if year == "" {
		validation = "2024" + date
	}
	if _, err := time.Parse(time.DateOnly, validation); err != nil {
		return ""
	}
	return date
}

func parseFactDate(value string, now time.Time) (time.Time, error) {
	if strings.HasPrefix(value, "-") {
		value = strconv.Itoa(now.Year()) + value
	}
	return time.ParseInLocation(time.DateOnly, value, now.Location())
}

func normalizeNumber(value string) string {
	return strings.NewReplacer(" ", "", "\u00a0", "", ",", ".").Replace(value)
}

func needsPrimary(text string, facts []model.Fact) bool {
	return len(facts) > 0 || containsPhrase(text, []string{
		"закон принят", "приняли закон", "утвержден закон", "вступил в силу", "вступит в силу",
		"вступает в силу", "вступают в силу", "требования закона", "принято постановление", "подписан указ",
		"новая ставка налога", "обязательная маркировка", "is now mandatory", "takes effect",
		"law was passed", "approved a law",
	})
}

func hasConfirmation(facts []model.Fact) bool {
	for _, fact := range facts {
		if fact.Key == "confirmation" {
			return true
		}
	}
	return false
}

func hasNewFact(existing, findings []model.Fact) bool {
	for _, fact := range findings {
		if !containsFact(existing, fact.Key, fact.Value) {
			return true
		}
	}
	return false
}

func containsFact(facts []model.Fact, key, value string) bool {
	for _, fact := range facts {
		if fact.Key == key && fact.Value == value {
			return true
		}
	}
	return false
}

// New observations replace earlier values for the fields they report. Source
// evidence for earlier releases persists in the publication history.
func mergeFacts(existing, findings []model.Fact) []model.Fact {
	replaced := make(map[string]bool)
	for _, fact := range findings {
		if !containsFact(existing, fact.Key, fact.Value) {
			replaced[fact.Key] = true
		}
	}
	merged := make([]model.Fact, 0, len(existing)+len(findings))
	for _, fact := range existing {
		if !replaced[fact.Key] {
			merged = append(merged, fact)
		}
	}
	for _, fact := range findings {
		if replaced[fact.Key] {
			merged = append(merged, fact)
		}
	}
	return merged
}
