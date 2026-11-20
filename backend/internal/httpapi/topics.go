package httpapi

import (
	"net/http"

	"smmagent/internal/channels"
	"smmagent/internal/model"
)

func previewTopic(service *channels.Service) http.HandlerFunc {
	return topicResource(func(w http.ResponseWriter, r *http.Request, channelID, materialID int64) {
		result, err := service.PreviewTopic(r.Context(), channelID, materialID)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

func reviewTopic(service *channels.Service) http.HandlerFunc {
	return topicResource(func(w http.ResponseWriter, r *http.Request, channelID, materialID int64) {
		var input struct {
			Verdict model.TopicVerdict `json:"verdict"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := service.ReviewTopic(r.Context(), channelID, materialID, input.Verdict); err != nil {
			serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func topicResource(handle func(http.ResponseWriter, *http.Request, int64, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelID, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		materialID, ok := parseID(w, "material_id", r.PathValue("materialID"))
		if ok {
			handle(w, r, channelID, materialID)
		}
	}
}
