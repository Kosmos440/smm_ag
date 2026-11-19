package news

import (
	"regexp"
	"time"

	"smmagent/internal/model"
)

const (
	trilateralMeetingKey = "meeting_subject"
	trilateralMeeting    = "russia-us-china"
	meetingMilestoneKey  = "meeting_milestone"
)

var (
	meetingLanguage    = regexp.MustCompile(`(?:^| )(?:встреч|переговор|саммит)[а-я]*(?: |$)`)
	trilateralLanguage = regexp.MustCompile(`(?:^| )(?:трехсторон[а-я]*|три лидера|трех лидеров)(?: |$)`)
	bilateralLanguage  = regexp.MustCompile(`(?:^| )двусторон[а-я]*(?: |$)`)
	meetingCompleted   = regexp.MustCompile(`(?:^| )(?:встреч[а-я]*|переговор[а-я]*|саммит[а-я]*)(?: [а-я0-9]+){0,10} (?:завершил[а-я]*|закончил[а-я]*|состоял[а-я]*)(?: |$)|(?:^| )(?:завершил[а-я]*|закончил[а-я]*|состоял[а-я]*) (?:трехсторон[а-я]* )?(?:встреч[а-я]*|переговор[а-я]*|саммит[а-я]*)(?: |$)`)
	jointResult        = regexp.MustCompile(`(?:^| )(?:подписал[а-я]*|опубликовал[а-я]*|выпустил[а-я]*) (?:текст )?(?:совместн[а-я]* (?:заявлен[а-я]*|деклараци[а-я]*|коммюнике)|деклараци[а-я]*|коммюнике)(?: |$)|(?:^| )(?:совместн[а-я]* (?:заявлен[а-я]*|деклараци[а-я]*|коммюнике)|деклараци[а-я]*|коммюнике) (?:было )?(?:подписан[а-я]*|опубликован[а-я]*)(?: |$)`)
	afterMeeting       = regexp.MustCompile(`(?:^| )(?:по итогам|после) (?:трехсторон[а-я]* )?(?:встреч[а-я]*|переговор[а-я]*|саммит[а-я]*)(?: |$)`)
	spokenResult       = regexp.MustCompile(`(?:^| )(путин[а-я]*|трамп[а-я]*|си цзиньпин[а-я]*) (?:уже )?(?:заявил|сообщил|рассказал|подтвердил|объявил|выступил|назвал|подвел)[а-я]*(?: |$)`)
	uncertainMeeting   = regexp.MustCompile(`(?:^| )(?:якобы|вероятно|предположительно|могут|может|ожидается|не завершил[а-я]*|не состоял[а-я]*|не подписал[а-я]*|не опубликовал[а-я]*)(?: |$)`)
)

// trilateralReport recognises one recurring negotiation format. A past-tense
// milestone is taken from the headline or lead, with attribution kept in the post.
func trilateralReport(title, body string) (bool, string) {
	title = normalize(title)
	lead := []rune(body)
	text := title + " " + normalize(string(lead[:min(len(lead), 420)]))
	if !meetingLanguage.MatchString(title) || bilateralLanguage.MatchString(title) ||
		(!trilateralLanguage.MatchString(text) && !mentionsTrilateralParticipants(title)) {
		return false, ""
	}
	if !mentionsTrilateralParticipants(text) {
		return false, ""
	}
	if uncertainMeeting.MatchString(title) {
		return true, ""
	}
	// Keep the claim in the headline. A speculative headline often quotes a
	// completed meeting in its background paragraphs.
	switch {
	case jointResult.MatchString(title):
		return true, "joint_statement"
	case afterMeeting.MatchString(title):
		if speaker := spokenResult.FindStringSubmatch(title); speaker != nil {
			return true, "statement:" + speaker[1]
		}
	case meetingCompleted.MatchString(title):
		return true, "meeting_held"
	}
	return true, ""
}

func mentionsTrilateralParticipants(text string) bool {
	return containsPhrase(text, []string{"россии", "россия", "рф", "путин", "путина"}) &&
		containsPhrase(text, []string{"сша", "соединенных штатов", "трамп", "трампа"}) &&
		containsPhrase(text, []string{"кнр", "китай", "китая", "си цзиньпин", "си цзиньпина"})
}

func recentTrilateralEvent(candidates []model.Event, now time.Time) (model.Event, bool) {
	var best model.Event
	for _, candidate := range candidates {
		if candidate.UpdatedAt.Before(now.Add(-36 * time.Hour)) {
			continue
		}
		if containsFact(candidate.Facts, trilateralMeetingKey, trilateralMeeting) {
			return candidate, true
		}
		recognised, _ := trilateralReport(candidate.Title, candidate.Summary)
		if recognised && (best.ID == 0 || candidate.UpdatedAt.After(best.UpdatedAt)) {
			best = candidate
		}
	}
	return best, best.ID != 0
}

func evaluateTrilateral(material model.Material, organization, milestone string, event model.Event, found bool, now time.Time) model.Evaluation {
	// Older cards may already represent this milestone without its structured key.
	sameMilestone := containsFact(event.Facts, meetingMilestoneKey, milestone)
	if found && !sameMilestone && milestone != "" {
		_, previous := trilateralReport(event.Title, event.Summary)
		sameMilestone = milestone == previous
	}
	if found && (milestone == "" || sameMilestone) {
		return model.Evaluation{Event: event, Accepted: true, Reason: "известная стадия встречи"}
	}
	if !found {
		event.FirstSeen = now
		event.Facts = []model.Fact{
			{Key: "report", Value: material.Title, URL: material.URL, Organization: organization},
			{Key: trilateralMeetingKey, Value: trilateralMeeting, URL: material.URL, Organization: organization},
		}
	} else {
		event.Facts = append([]model.Fact(nil), event.Facts...)
		if !containsFact(event.Facts, trilateralMeetingKey, trilateralMeeting) {
			event.Facts = append(event.Facts, model.Fact{Key: trilateralMeetingKey, Value: trilateralMeeting, URL: material.URL, Organization: organization})
			// Record the prior milestone when upgrading an older card.
			if _, previous := trilateralReport(event.Title, event.Summary); previous != "" {
				event.Facts = append(event.Facts, model.Fact{Key: meetingMilestoneKey, Value: previous, Organization: organization})
			}
		}
	}
	event.Title, event.Summary, event.UpdatedAt = material.Title, shortSummary(material.Title, material.Body), now
	if milestone == "" {
		return model.Evaluation{Event: event, Accepted: true, Changed: true, Priority: 30, Reason: "сообщение о встрече"}
	}
	event.Facts = append(event.Facts, model.Fact{
		Key: meetingMilestoneKey, Value: milestone, URL: material.URL, Organization: organization,
	})
	return model.Evaluation{Event: event, Accepted: true, Changed: true, Urgent: true,
		Priority: 85, Reason: "новый итог трёхсторонней встречи"}
}
