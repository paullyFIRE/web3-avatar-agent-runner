package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
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
	cfg        *config.Config
	db         *db.DB
	templates  *template.Template
}

func New(cfg *config.Config, database *db.DB) *Server {
	s := &Server{cfg: cfg, db: database}
	s.templates = parseTemplates(cfg)
	return s
}

func parseTemplates(cfg *config.Config) *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"jobClass": func(state string) string {
			switch state {
			case "queued", "retry_scheduled":
				return "bg-yellow-100 text-yellow-800"
			case "waiting_for_review":
				return "bg-blue-100 text-blue-800"
			case "failed", "blocked", "closed_without_merge":
				return "bg-red-100 text-red-800"
			case "merged", "cleanup_done":
				return "bg-green-100 text-green-800"
			case "running_agent", "preparing_worktree", "validating", "committing", "pushing", "creating_pr", "applying_pr_feedback", "cleanup_running":
				return "bg-purple-100 text-purple-800"
			default:
				return "bg-gray-100 text-gray-800"
			}
		},
		"formatTime": func(t interface{}) string {
			switch v := t.(type) {
			case *time.Time:
				if v == nil {
					return "-"
				}
				return v.Format("2006-01-02 15:04:05")
			case time.Time:
				if v.IsZero() {
					return "-"
				}
				return v.Format("2006-01-02 15:04:05")
			default:
				return "-"
			}
		},
		"formatDuration": func(started, finished *time.Time) string {
			if started == nil || started.IsZero() {
				return "-"
			}
			end := time.Now()
			if finished != nil && !finished.IsZero() {
				end = *finished
			}
			d := end.Sub(*started)
			if d < 0 {
				return "-"
			}
			if d < time.Minute {
				return fmt.Sprintf("%ds", int(d.Seconds()))
			}
			if d < time.Hour {
				return fmt.Sprintf("%dm", int(d.Minutes()))
			}
			return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
		},
		"issueURL": func(n int) string {
			return fmt.Sprintf("https://github.com/%s/%s/issues/%d", cfg.GitHubOwner, cfg.GitHubRepo, n)
		},
		"prURL": func(n int) string {
			return fmt.Sprintf("https://github.com/%s/%s/pull/%d", cfg.GitHubOwner, cfg.GitHubRepo, n)
		},
		"json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
		"hasPrefix": func(s, prefix string) bool {
			return strings.HasPrefix(s, prefix)
		},
		"hasError": func(s *string) bool {
			return s != nil && *s != ""
		},
	}).ParseFS(templateFS, "*.html"))
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/dashboard", s.dashboard)
	r.Get("/jobs", s.jobsList)
	r.Get("/jobs/{id}", s.jobDetail)
	r.Get("/jobs/{id}/logs", s.jobLogs)
	r.Get("/agents", s.agentsList)
	r.Post("/jobs/{id}/retry", s.jobRetry)
	r.Post("/jobs/{id}/cancel", s.jobCancel)
	r.Post("/jobs/{id}/cleanup", s.jobCleanup)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	return r
}

type DashboardData struct {
	TotalJobs     int
	QueuedJobs    int
	RunningJobs   int
	FailedJobs    int
	WaitingJobs   int
	MergedJobs    int
	RecentJobs    []*db.Job
	RetryJobs     int
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.ListJobs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := DashboardData{
		TotalJobs: len(jobs),
	}

	for _, j := range jobs {
		switch j.State {
		case "queued", "retry_scheduled":
			data.QueuedJobs++
		case "preparing_worktree", "running_agent", "validating", "committing", "pushing", "creating_pr", "applying_pr_feedback", "cleanup_running":
			data.RunningJobs++
		case "failed", "blocked":
			data.FailedJobs++
		case "waiting_for_review":
			data.WaitingJobs++
		case "merged", "closed_without_merge", "cleanup_done":
			data.MergedJobs++
		}
		if j.State == "retry_scheduled" {
			data.RetryJobs++
		}
	}

	if len(jobs) > 20 {
		jobs = jobs[:20]
	}
	data.RecentJobs = jobs

	s.renderTemplate(w, "dashboard", data)
}

func (s *Server) jobsList(w http.ResponseWriter, r *http.Request) {
	stateFilter := r.URL.Query().Get("state")

	var jobs []*db.Job
	var err error

	if stateFilter != "" {
		jobs, err = s.db.GetJobsByState(r.Context(), strings.Split(stateFilter, ",")...)
	} else {
		jobs, err = s.db.ListJobs(r.Context())
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "jobs", jobs)
}

func (s *Server) jobDetail(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	job, err := s.db.GetJob(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	stateLogs, err := s.db.GetStateLogs(r.Context(), id)
	if err != nil {
		slog.Error("get state logs", "error", err)
	}

	s.renderTemplate(w, "job_detail", map[string]interface{}{
		"Job":  job,
		"Logs": stateLogs,
	})
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

	currentLog := filepath.Join(s.cfg.LogDir, fmt.Sprintf("job-%d-attempt-%d.log", id, job.Attempt))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	// Send all previous attempt logs
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

	// Send current attempt header and content
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

func (s *Server) agentsList(w http.ResponseWriter, r *http.Request) {
	// For now, render a list of running jobs as "agents"
	jobs, err := s.db.GetJobsByState(r.Context(), "running_agent", "applying_pr_feedback")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "agents", jobs)
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
	http.Redirect(w, r, "/jobs/"+idStr, http.StatusFound)
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
	http.Redirect(w, r, "/jobs/"+idStr, http.StatusFound)
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
	http.Redirect(w, r, "/jobs/"+idStr, http.StatusFound)
}

func strPtr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render template", "name", name, "error", err)
		http.Error(w, fmt.Sprintf("template error: %s", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

//go:embed *.html
var templateFS embed.FS
