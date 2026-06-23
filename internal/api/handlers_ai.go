package api

import (
	"net/http"
	"strings"

	"github.com/open-edge-platform/image-composer-tool/internal/ai/rag"
)

// queryRequest matches the OpenAPI QueryRequest schema.
type queryRequest struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id,omitempty"`
}

// queryResponse matches the OpenAPI QueryResponse schema (Phase 1 subset).
// Fields that require session support (changes, full validation) are omitted
// for now and will be added in Phase 3 without breaking changes.
type queryResponse struct {
	YAML             string             `json:"yaml"`
	SearchResults    []searchResultJSON `json:"search_results"`
	SourceTemplates  []string           `json:"source_templates"`
	GenerationTimeMs int64              `json:"generation_time_ms"`
}

// searchResponse matches the OpenAPI SearchResponse schema.
type searchResponse struct {
	Results []searchResultJSON `json:"results"`
	Query   string             `json:"query"`
}

// searchResultJSON matches the OpenAPI SearchResult schema.
type searchResultJSON struct {
	Template      templateInfoJSON `json:"template"`
	Score         float64          `json:"score"`
	SemanticScore float64          `json:"semantic_score"`
	KeywordScore  float64          `json:"keyword_score"`
	PackageScore  float64          `json:"package_score"`
}

// templateInfoJSON matches the OpenAPI TemplateInfo schema.
type templateInfoJSON struct {
	FileName     string           `json:"file_name"`
	ImageName    string           `json:"image_name"`
	ImageVersion string           `json:"image_version"`
	Distribution string           `json:"distribution"`
	Architecture string           `json:"architecture"`
	OS           string           `json:"os"`
	ImageType    string           `json:"image_type"`
	Packages     []string         `json:"packages"`
	Metadata     templateMetaJSON `json:"metadata"`
}

// templateMetaJSON matches the OpenAPI TemplateMetadata schema.
type templateMetaJSON struct {
	Description    string   `json:"description,omitempty"`
	UseCases       []string `json:"use_cases,omitempty"`
	Keywords       []string `json:"keywords,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	RecommendedFor []string `json:"recommended_for,omitempty"`
}

// handleQuery handles non-streaming AI template generation.
// POST /api/v1/ai/query
//
// Accepts a natural language query and returns the generated YAML template.
// In Phase 1, session_id is accepted but not acted upon (sessions are Phase 3).
func handleQuery(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req queryRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		// Validate query is present and within length limits.
		query := strings.TrimSpace(req.Query)
		if query == "" {
			respondError(w, http.StatusBadRequest, ErrCodeQueryRequired,
				"A query string is required", nil)
			return
		}
		if len(query) > maxQueryLength {
			respondError(w, http.StatusBadRequest, ErrCodeQueryTooLong,
				"Query exceeds 2000 characters",
				map[string]any{"max_length": maxQueryLength})
			return
		}

		ctx := r.Context()

		// Call the existing Go library directly — no CLI, no subprocess.
		yaml, err := s.engine.Generate(ctx, query)
		if err != nil {
			// Determine if this is a provider connectivity issue or a
			// generation failure.
			errMsg := err.Error()
			if strings.Contains(errMsg, "connect") || strings.Contains(errMsg, "connection") {
				respondError(w, http.StatusServiceUnavailable, ErrCodeProviderUnavail,
					"AI provider not reachable: "+errMsg, nil)
				return
			}
			respondError(w, http.StatusBadGateway, ErrCodeGenerationFailed,
				"Template generation failed: "+errMsg, nil)
			return
		}

		resp := queryResponse{
			YAML:            yaml,
			SearchResults:   []searchResultJSON{},  // Populated in future when Generate returns richer data
			SourceTemplates: []string{},             // Populated in future when Generate returns richer data
		}

		respondJSON(w, http.StatusOK, resp)
	}
}

// handleSearch handles semantic template search.
// GET /api/v1/ai/search?query=...
//
// Performs a hybrid search (semantic + keyword + package) against the
// indexed template library and returns the top 5 results.
func handleSearch(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("query"))

		if query == "" {
			respondError(w, http.StatusBadRequest, ErrCodeQueryRequired,
				"A query string is required", nil)
			return
		}
		if len(query) > maxQueryLength {
			respondError(w, http.StatusBadRequest, ErrCodeQueryTooLong,
				"Query exceeds 2000 characters",
				map[string]any{"max_length": maxQueryLength})
			return
		}

		ctx := r.Context()

		// Call the existing Go library directly — no CLI, no subprocess.
		results, err := s.engine.Search(ctx, query)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "connect") || strings.Contains(errMsg, "connection") {
				respondError(w, http.StatusServiceUnavailable, ErrCodeProviderUnavail,
					"AI provider not reachable: "+errMsg, nil)
				return
			}
			respondError(w, http.StatusBadGateway, ErrCodeSearchFailed,
				"Template search failed: "+errMsg, nil)
			return
		}

		// Convert internal SearchResult structs to the OpenAPI JSON shape.
		jsonResults := make([]searchResultJSON, 0, len(results))
		for _, sr := range results {
			jsonResults = append(jsonResults, convertSearchResult(sr))
		}

		resp := searchResponse{
			Results: jsonResults,
			Query:   query,
		}

		respondJSON(w, http.StatusOK, resp)
	}
}

// convertSearchResult converts a rag.SearchResult to the OpenAPI JSON shape.
func convertSearchResult(sr rag.SearchResult) searchResultJSON {
	info := templateInfoJSON{
		FileName:     sr.Template.FileName,
		ImageName:    sr.Template.ImageName,
		ImageVersion: sr.Template.ImageVersion,
		Distribution: sr.Template.Distribution,
		Architecture: sr.Template.Architecture,
		OS:           sr.Template.OS,
		ImageType:    sr.Template.ImageType,
		Packages:     sr.Template.Packages,
		Metadata: templateMetaJSON{
			Description:    sr.Template.Metadata.Description,
			UseCases:       sr.Template.Metadata.UseCases,
			Keywords:       sr.Template.Metadata.Keywords,
			Capabilities:   sr.Template.Metadata.Capabilities,
			RecommendedFor: sr.Template.Metadata.RecommendedFor,
		},
	}

	// Ensure non-nil slices in JSON output ([] instead of null).
	if info.Packages == nil {
		info.Packages = []string{}
	}
	if info.Metadata.UseCases == nil {
		info.Metadata.UseCases = []string{}
	}
	if info.Metadata.Keywords == nil {
		info.Metadata.Keywords = []string{}
	}
	if info.Metadata.Capabilities == nil {
		info.Metadata.Capabilities = []string{}
	}
	if info.Metadata.RecommendedFor == nil {
		info.Metadata.RecommendedFor = []string{}
	}

	return searchResultJSON{
		Template:      info,
		Score:         sr.Score,
		SemanticScore: sr.SemanticScore,
		KeywordScore:  sr.KeywordScore,
		PackageScore:  sr.PackageScore,
	}
}
