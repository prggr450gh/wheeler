package web

import (
	"encoding/json"
	"log"
	"net/http"
)

// configPageHandler serves the config admin page
func (s *Server) configPageHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("[CONFIG] Handling config page request")

	symbols, err := s.symbolService.GetDistinctSymbols()
	if err != nil {
		log.Printf("[CONFIG] Error getting symbols: %v", err)
		symbols = []string{}
	}

	config, err := s.configService.GetAll()
	if err != nil {
		log.Printf("[CONFIG] Error getting config: %v", err)
	}

	data := ConfigPageData{
		AllSymbols: symbols,
		CurrentDB:  s.getCurrentDatabaseName(),
		ActivePage: "config",
		Config:     config,
	}

	s.renderTemplate(w, "config.html", data)
}

// configAPIHandler routes GET and PUT requests for /api/config
func (s *Server) configAPIHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config, err := s.configService.GetAll()
		if err != nil {
			log.Printf("[CONFIG API] Error getting config: %v", err)
			http.Error(w, "Failed to get config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)

	case http.MethodPut:
		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		updated, err := s.configService.Set(req.Key, req.Value)
		if err != nil {
			log.Printf("[CONFIG API] Error setting config key %s: %v", req.Key, err)
			http.Error(w, "Failed to update config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
