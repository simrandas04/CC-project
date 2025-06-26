package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"raft3d/models"
	raftnode "raft3d/raft"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/raft"
)

var (
	fsm      *raftnode.FSM
	myNodeID string
)

func Init(f *raftnode.FSM, nodeID string) {
	fsm = f
	myNodeID = nodeID
}

func generateID(prefix string, n int) string {
	return fmt.Sprintf("%s-%d", prefix, n+1)
}

// Apply a command to the Raft cluster or locally for testing
func applyCommand(cmdType string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	cmd := raftnode.Command{Type: cmdType, Data: b}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	// If we're the leader, apply through Raft
	if raftnode.RaftNode.State() == raft.Leader {
		future := raftnode.RaftNode.Apply(cmdBytes, 5*time.Second)
		if err := future.Error(); err != nil {
			return err
		}
	} else {
		// For non-leader nodes, return an error - requests should go to leader
		return errors.New("not the leader - forward request to current leader")
	}

	return nil
}

// HandlePrinters handles both GET and POST requests for printers
func HandlePrinters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		// Return all printers
		printers := make([]models.Printer, 0, len(fsm.Printers))
		for _, p := range fsm.Printers {
			printers = append(printers, p)
		}
		json.NewEncoder(w).Encode(printers)

	case http.MethodPost:
		// Create a new printer
		var p models.Printer
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		p.ID = generateID("printer", len(fsm.Printers))

		if err := applyCommand("printer:create", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(p)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleFilaments handles both GET and POST requests for filaments
func HandleFilaments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		// Return all filaments
		filaments := make([]models.Filament, 0, len(fsm.Filaments))
		for _, f := range fsm.Filaments {
			filaments = append(filaments, f)
		}
		json.NewEncoder(w).Encode(filaments)

	case http.MethodPost:
		// Create a new filament
		var f models.Filament
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		f.ID = generateID("filament", len(fsm.Filaments))

		// Initialize RemainingWeightInGrams if not set
		if f.RemainingWeightInGrams == 0 {
			f.RemainingWeightInGrams = f.TotalWeightInGrams
		}

		if err := applyCommand("filament:create", f); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(f)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// calculateUsedFilamentWeight calculates the total weight of filament used
// by all queued or running print jobs for a specific filament
func calculateUsedFilamentWeight(filamentID string) int {
	totalWeight := 0
	for _, job := range fsm.PrintJobs {
		if job.FilamentID == filamentID && (job.Status == "Queued" || job.Status == "Running") {
			totalWeight += job.PrintWeightInGrams
		}
	}
	return totalWeight
}

// HandlePrintJobs handles both GET and POST requests for print jobs
func HandlePrintJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		// Get status filter if provided
		status := r.URL.Query().Get("status")

		printJobs := make([]models.PrintJob, 0)
		for _, job := range fsm.PrintJobs {
			// Filter by status if specified
			if status != "" && job.Status != status {
				continue
			}
			printJobs = append(printJobs, job)
		}

		json.NewEncoder(w).Encode(printJobs)

	case http.MethodPost:
		// Create a new print job
		var job models.PrintJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Validate printer exists
		if _, exists := fsm.Printers[job.PrinterID]; !exists {
			http.Error(w, "Invalid printer ID", http.StatusBadRequest)
			return
		}

		// Validate filament exists
		filament, exists := fsm.Filaments[job.FilamentID]
		if !exists {
			http.Error(w, "Invalid filament ID", http.StatusBadRequest)
			return
		}

		// Calculate weight used by other queued/running jobs
		usedWeight := calculateUsedFilamentWeight(job.FilamentID)

		// Check if there's enough filament
		if job.PrintWeightInGrams > (filament.RemainingWeightInGrams - usedWeight) {
			http.Error(w, fmt.Sprintf("Not enough filament remaining. Required: %d, Available: %d",
				job.PrintWeightInGrams, filament.RemainingWeightInGrams-usedWeight), http.StatusBadRequest)
			return
		}

		// Set job ID and initial status
		job.ID = generateID("job", len(fsm.PrintJobs))
		job.Status = "Queued" // Always start in Queued state

		// Apply the command
		if err := applyCommand("printjob:create", job); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(job)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandlePrintJobStatus updates the status of a print job
func HandlePrintJobStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse job ID from URL path
	// Expecting: /api/v1/print_jobs/{id}/status?status=running
	re := regexp.MustCompile(`/api/v1/print_jobs/([^/]+)/status`)
	matches := re.FindStringSubmatch(r.URL.Path)

	if len(matches) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}

	jobID := matches[1]
	newStatus := r.URL.Query().Get("status")

	if newStatus == "" {
		http.Error(w, "Missing status parameter", http.StatusBadRequest)
		return
	}

	// Remove quotes if present
	newStatus = strings.Trim(newStatus, "\"")

	// Find the job
	job, exists := fsm.PrintJobs[jobID]
	if !exists {
		http.Error(w, "Print job not found", http.StatusNotFound)
		return
	}

	// Validate state transition
	if err := validateStatusTransition(job.Status, newStatus); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Update job status
	job.Status = newStatus

	// If transitioning to Done state, update filament remaining weight
	if newStatus == "Done" {
		filament := fsm.Filaments[job.FilamentID]
		filament.RemainingWeightInGrams -= job.PrintWeightInGrams

		// Apply filament update command
		if err := applyCommand("filament:update", filament); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Apply job update command
	if err := applyCommand("printjob:update", job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(job)
}

// validateStatusTransition checks if a status transition is valid
func validateStatusTransition(currentStatus, newStatus string) error {
	// Validate allowed transitions
	switch currentStatus {
	case "Queued":
		if newStatus != "Running" && newStatus != "Canceled" {
			return errors.New("a job can only transition from Queued to Running or Canceled")
		}
	case "Running":
		if newStatus != "Done" && newStatus != "Canceled" {
			return errors.New("a job can only transition from Running to Done or Canceled")
		}
	case "Done", "Canceled":
		return errors.New("cannot change status of a job that is already Done or Canceled")
	default:
		return errors.New("invalid current status")
	}

	return nil
}

// JoinHandler handles cluster membership changes
func JoinHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Only the leader can add new nodes
	if raftnode.RaftNode.State() != raft.Leader {
		leaderAddr, _ := raftnode.RaftNode.LeaderWithID()
		http.Error(w, fmt.Sprintf("Not the leader, current leader is at %s", leaderAddr), http.StatusTemporaryRedirect)
		return
	}

	configFuture := raftnode.RaftNode.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Check if node is already in the cluster
	for _, srv := range configFuture.Configuration().Servers {
		if srv.ID == raft.ServerID(req.ID) || srv.Address == raft.ServerAddress(req.Address) {
			w.WriteHeader(http.StatusOK)
			return // already part of cluster
		}
	}

	// Add the new node as a voter
	addFuture := raftnode.RaftNode.AddVoter(raft.ServerID(req.ID), raft.ServerAddress(req.Address), 0, 0)
	if err := addFuture.Error(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Node %s at %s joined the cluster", req.ID, req.Address)
	w.WriteHeader(http.StatusOK)
}

// GetClusterStatus returns information about the Raft cluster
func GetClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	leaderID, leaderAddr := raftnode.RaftNode.LeaderWithID()

	// Get configuration
	configFuture := raftnode.RaftNode.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build server list
	servers := make([]map[string]string, 0)
	for _, srv := range configFuture.Configuration().Servers {
		servers = append(servers, map[string]string{
			"id":      string(srv.ID),
			"address": string(srv.Address),
			"type":    string(srv.Suffrage),
		})
	}

	status := map[string]interface{}{
		"leader": map[string]string{
			"id":      string(leaderID),
			"address": string(leaderAddr),
		},
		"state":   raftnode.RaftNode.State().String(),
		"servers": servers,
	}

	json.NewEncoder(w).Encode(status)
}

// WhoAmI returns the current node's Raft state and leader information
func WhoAmI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	_, leaderAddr := raftnode.RaftNode.LeaderWithID()

	info := map[string]string{
		"state":  raftnode.RaftNode.State().String(),
		"leader": string(leaderAddr),
		"myID":   myNodeID,
	}

	json.NewEncoder(w).Encode(info)
}
