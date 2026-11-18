// Package httpapi exposes the local management API and the published news feed.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"smmagent/internal/channels"
	"smmagent/internal/model"
)

const prefix = "/api/v1"

func New(service *channels.Service, ready func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	registerConsole(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "Database unavailable.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	resource(mux, prefix+"/channels", service.ListChannels, service.GetChannel, service.CreateChannel,
		service.UpdateChannel, func(channel model.Channel) int64 { return channel.ID })
	mux.HandleFunc("POST "+prefix+"/filter-suggestions", func(w http.ResponseWriter, r *http.Request) {
		var input channels.FilterRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := service.SuggestFilters(r.Context(), input)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	resource(mux, prefix+"/sources", service.ListSources, service.GetSource, service.CreateSource,
		service.UpdateSource, func(source model.Source) int64 { return source.ID })
	mux.HandleFunc("POST "+prefix+"/sources/{id}/probe", probeSource(service))
	resource(mux, prefix+"/presets", service.ListPresets, service.GetPreset, service.CreatePreset,
		service.UpdatePreset, func(preset model.Preset) int64 { return preset.ID })
	mux.HandleFunc("GET "+prefix+"/channels/{id}/events", channelCollection(service, service.ListEvents))
	mux.HandleFunc("GET "+prefix+"/channels/{id}/publications", channelCollection(service, service.ListPublications))
	mux.HandleFunc("GET "+prefix+"/channels/{id}/topic-evaluations", channelCollection(service, service.ListTopics))
	mux.HandleFunc("POST "+prefix+"/channels/{id}/topic-evaluations/{materialID}/preview", previewTopic(service))
	mux.HandleFunc("PUT "+prefix+"/channels/{id}/topic-evaluations/{materialID}/review", reviewTopic(service))
	mux.HandleFunc("GET "+prefix+"/review", collection(service.ListReview))
	mux.HandleFunc("PUT "+prefix+"/review/{id}", review(service))
	mux.HandleFunc("GET "+prefix+"/site/news", siteNews(service))
	return mux
}

func probeSource(service *channels.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		items, err := service.ProbeSource(r.Context(), id)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Items []channels.SourcePreview `json:"items"`
		}{Items: items})
	}
}

func resource[T, Input any](
	mux *http.ServeMux,
	path string,
	list func(context.Context, int, int) ([]T, error),
	get func(context.Context, int64) (T, error),
	create func(context.Context, Input) (T, error),
	update func(context.Context, int64, Input) (T, error),
	id func(T) int64,
) {
	mux.HandleFunc("GET "+path, collection(list))
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var input Input
		if !decodeJSON(w, r, &input) {
			return
		}
		item, err := create(r.Context(), input)
		if err != nil {
			serviceError(w, err)
			return
		}
		w.Header().Set("Location", fmt.Sprintf("%s/%d", path, id(item)))
		writeJSON(w, http.StatusCreated, item)
	})
	mux.HandleFunc("GET "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		item, err := get(r.Context(), id)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	mux.HandleFunc("PUT "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		var input Input
		if !decodeJSON(w, r, &input) {
			return
		}
		item, err := update(r.Context(), id, input)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
}

func collection[T any](list func(context.Context, int, int) ([]T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		items, err := list(r.Context(), limit, offset)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeList(w, items, limit, offset)
	}
}

func channelCollection[T any](
	service *channels.Service,
	list func(context.Context, int64, int, int) ([]T, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		if _, err := service.GetChannel(r.Context(), id); err != nil {
			serviceError(w, err)
			return
		}
		items, err := list(r.Context(), id, limit, offset)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeList(w, items, limit, offset)
	}
}

func review(service *channels.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, "id", r.PathValue("id"))
		if !ok {
			return
		}
		var input struct {
			Decision string `json:"decision"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		item, err := service.Review(r.Context(), id, input.Decision)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func siteNews(service *channels.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var id int64
		query := r.URL.Query()
		if query.Has("channel_id") {
			var ok bool
			id, ok = parseID(w, "channel_id", query.Get("channel_id"))
			if !ok {
				return
			}
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		items, err := service.SiteNews(r.Context(), id, limit, offset)
		if err != nil {
			serviceError(w, err)
			return
		}
		writeList(w, items, limit, offset)
	}
}

func serviceError(w http.ResponseWriter, err error) {
	var invalid *channels.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, "invalid_input", invalid.Error())
	case errors.Is(err, model.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, model.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "Resource conflicts with its current state or an existing item.")
	case errors.Is(err, channels.ErrProbeUnavailable):
		writeError(w, http.StatusServiceUnavailable, "probe_unavailable", "Source probe unavailable.")
	case errors.Is(err, channels.ErrProbeFailed):
		writeError(w, http.StatusBadGateway, "probe_failed", "Could not fetch or parse the source.")
	case errors.Is(err, channels.ErrFilterGeneratorUnavailable):
		writeError(w, http.StatusServiceUnavailable, "filter_generator_unavailable", "Подключите модель для подбора фильтров.")
	case errors.Is(err, channels.ErrFilterGenerationFailed):
		writeError(w, http.StatusBadGateway, "filter_generation_failed", "Модель не вернула подходящий список фильтров. Попробуйте ещё раз.")
	default:
		// Database driver errors can contain input values. Keep those out of
		// both the network response and the local API log.
		log.Printf("HTTP service operation failed (%T)", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
	}
}
