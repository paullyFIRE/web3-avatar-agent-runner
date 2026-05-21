package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
)

type Server struct {
	cfg *config.Config
	db  *db.DB
}

func New(cfg *config.Config, database *db.DB) *Server {
	return &Server{cfg: cfg, db: database}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Plain-text log endpoint (used by both old and new UI polling)
	r.Get("/jobs/{id}/logs", s.jobLogs)

	// POST actions (redirect to the SPA after completion)
	r.Post("/jobs/{id}/retry", s.jobRetry)
	r.Post("/jobs/{id}/cancel", s.jobCancel)
	r.Post("/jobs/{id}/cleanup", s.jobCleanup)

	// SPA — serve files from frontend/dist/; any unmatched route serves index.html for client-side routing
	spaDir := "frontend/dist"
	r.Get("/ui/*", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if len(path) > 3 {
			path = path[3:]
		}
		fullPath := filepath.Join(spaDir, path)
		if fi, err := os.Stat(fullPath); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, fullPath)
			return
		}
		http.ServeFile(w, r, filepath.Join(spaDir, "index.html"))
	})
	r.Get("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(spaDir, "index.html"))
	})

	// JSON API
	r.Get("/api/status", s.apiStatus)
	r.Get("/api/jobs", s.apiJobs)
	r.Get("/api/jobs/{id}", s.apiJobDetail)
	r.Get("/api/jobs/{id}/states", s.apiJobStates)
	r.Get("/api/jobs/{id}/logs", s.jobLogs)

	// Root redirects to the new UI
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusFound)
	})

	return r
}

func jsonResp(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.ListJobs(r.Context())
	if err != nil {
		jsonResp(w, map[string]interface{}{"error": err.Error()})
		return
	}
	var running, queued, failed, waiting, retry, total int
	total = len(jobs)
	for _, j := range jobs {
		switch j.State {
		case "queued":
			queued++
		case "retry_scheduled":
			retry++
		case "failed", "blocked":
			failed++
		case "waiting_for_review":
			waiting++
		case "running_agent", "preparing_worktree", "validating", "committing", "pushing", "creating_pr", "applying_pr_feedback", "cleanup_running":
			running++
		}
	}
	jsonResp(w, map[string]int{
		"total": total, "running": running, "queued": queued,
		"failed": failed, "waiting": waiting, "retry": retry,
	})
}

func (s *Server) apiJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.ListJobs(r.Context())
	if err != nil {
		jsonResp(w, map[string]string{"error": err.Error()})
		return
	}
	jsonResp(w, jobs)
}

func (s *Server) apiJobDetail(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonResp(w, map[string]string{"error": "invalid id"})
		return
	}
	job, err := s.db.GetJob(r.Context(), id)
	if err != nil || job == nil {
		jsonResp(w, map[string]string{"error": "not found"})
		return
	}
	jsonResp(w, job)
}

func (s *Server) apiJobStates(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonResp(w, map[string]string{"error": "invalid id"})
		return
	}
	logs, err := s.db.GetStateLogs(r.Context(), id)
	if err != nil {
		jsonResp(w, map[string]string{"error": err.Error()})
		return
	}
	jsonResp(w, logs)
}

func (s *Server) jobLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	job, _ := s.db.GetJob(r.Context(), id)
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	// Show process tree if PID is known
	if job.PID != nil && *job.PID > 0 {
		tree := getProcessTree(*job.PID)
		if tree != "" {
			io.WriteString(w, "━━━ Process Tree ━━━\n")
			io.WriteString(w, tree)
			io.WriteString(w, "\n\n")
		}
	}

	currentLog := filepath.Join(s.cfg.LogDir, fmt.Sprintf("job-%d-attempt-%d.log", id, job.Attempt))

	pattern := filepath.Join(s.cfg.LogDir, fmt.Sprintf("job-%d-attempt-*.log", id))
	prevLogs, _ := filepath.Glob(pattern)
	for _, p := range prevLogs {
		if p == currentLog {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		io.WriteString(w, fmt.Sprintf("\n━━━ Previous attempt: %s ━━━\n\n", filepath.Base(p)))
		w.Write(data)
		io.WriteString(w, "\n")
	}

	io.WriteString(w, fmt.Sprintf("\n━━━ Current attempt (attempt %d) ━━━\n\n", job.Attempt))

	data, err := os.ReadFile(currentLog)
	if err != nil {
		if os.IsNotExist(err) {
			io.WriteString(w, "No logs available yet.\n")
		}
	} else {
		w.Write(data)
	}
}

func getProcessTree(rootPid int) string {
	out, err := exec.Command("ps", "-o", "pid,ppid,state,etime,command", "--no-headers", "-p", fmt.Sprintf("%d", rootPid)).Output()
	if err != nil {
		return ""
	}
	// Find children by scanning all processes
	allOut, err := exec.Command("ps", "-o", "pid,ppid,state,etime,command", "--no-headers", "-e").Output()
	if err != nil {
		return string(out)
	}
	children := ""
	rootStr := fmt.Sprintf(" %d ", rootPid)
	for _, line := range strings.Split(string(allOut), "\n") {
		if strings.Contains(line, rootStr) && !strings.Contains(line, "ps -o") {
			pidStr := strings.Fields(line)
			if len(pidStr) > 1 && pidStr[1] == fmt.Sprintf("%d", rootPid) && pidStr[0] != fmt.Sprintf("%d", rootPid) {
				children += line + "\n"
			}
		}
	}
	if children != "" {
		return string(out) + "\n── Children ──\n" + children
	}
	return string(out)
}

func (s *Server) jobRetry(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	job, err := s.db.GetJob(r.Context(), id)
	if err != nil || job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	if job.State != "failed" && job.State != "blocked" && job.State != "retry_scheduled" && job.State != "needs_clarification" {
		http.Error(w, "job is not in a retriable state", http.StatusBadRequest)
		return
	}

	s.db.UpdateJob(r.Context(), id, db.JobUpdate{
		State:      strPtr("queued"),
		LastError:  nil,
		FinishedAt: nil,
	})
	http.Redirect(w, r, "/ui/jobs/"+idStr, http.StatusFound)
}

func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	s.db.UpdateJob(r.Context(), id, db.JobUpdate{
		State:      strPtr("failed"),
		LastError:  strPtr("cancelled by user"),
		FinishedAt: timePtr(time.Now()),
	})
	http.Redirect(w, r, "/ui/jobs/"+idStr, http.StatusFound)
}

func (s *Server) jobCleanup(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	s.db.UpdateJob(r.Context(), id, db.JobUpdate{
		State:      strPtr("cleanup_done"),
		FinishedAt: timePtr(time.Now()),
	})
	http.Redirect(w, r, "/ui/jobs/"+idStr, http.StatusFound)
}

func strPtr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }
