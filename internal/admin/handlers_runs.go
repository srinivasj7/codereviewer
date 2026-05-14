package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

type runRow struct {
	RunId     string
	RepoId    string
	PrNumber  int
	ShortSha  string
	Trigger   string
	Status    string
	ModelUsed string
	CostUsd   string
	Error     string
	StartedAt string
}

type runsView struct {
	viewData
	TenantId string
	Runs     []runRow
}

func (s *Server) handleRunsGET(w http.ResponseWriter, r *http.Request) {
	tenant := ports.TenantId(s.deps.Cfg.Tenant.Id)
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	rows, err := s.deps.PrRuns.ListAcrossRepos(r.Context(), tenant, limit)
	if err != nil {
		s.renderError(w, r, "runs", fmt.Errorf("list runs: %w", err))
		return
	}
	view := runsView{TenantId: string(tenant)}
	for _, run := range rows {
		view.Runs = append(view.Runs, runRow{
			RunId:     string(run.RunId),
			RepoId:    string(run.Ref.RepoId),
			PrNumber:  run.Ref.PrNumber,
			ShortSha:  shortSha(run.Ref.HeadSha),
			Trigger:   string(run.Trigger),
			Status:    string(run.Status),
			ModelUsed: run.ModelUsed,
			CostUsd:   fmt.Sprintf("%.4f", run.CostUsd),
			Error:     run.Error,
			StartedAt: run.StartedAt.Format(time.RFC3339),
		})
	}
	s.renderWith(w, r, "runs", &view)
}

func (s *Server) handleRunsRetryPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, "runs", err)
		return
	}
	runId := store.RunId(r.PostFormValue("run_id"))
	if runId == "" {
		http.Error(w, "run_id required", http.StatusBadRequest)
		return
	}
	run, found, err := s.deps.PrRuns.GetByRunId(r.Context(), runId)
	if err != nil {
		s.renderError(w, r, "runs", err)
		return
	}
	if !found {
		s.renderError(w, r, "runs", fmt.Errorf("run %s not found", runId))
		return
	}
	if s.deps.Bus == nil {
		s.renderError(w, r, "runs", fmt.Errorf("message bus not wired into admin"))
		return
	}
	if err := schemas.PublishReviewJob(r.Context(), s.deps.Bus, schemas.ReviewJob{
		PrRef:   run.Ref,
		Trigger: ports.TriggerManual,
	}); err != nil {
		s.renderError(w, r, "runs", fmt.Errorf("publish review job: %w", err))
		return
	}
	// Bounce back to /runs with the success flash.
	http.Redirect(w, r, "/runs", http.StatusFound)
}

func shortSha(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Repos page.

type reposViewRow struct {
	RepoId        string
	Owner         string
	Name          string
	DefaultBranch string
	Enabled       bool
}

type reposView struct {
	viewData
	Repos []reposViewRow
}

func (s *Server) handleReposGET(w http.ResponseWriter, r *http.Request) {
	tenant := ports.TenantId(s.deps.Cfg.Tenant.Id)
	repos, err := s.deps.Repos.ListByTenant(r.Context(), tenant)
	if err != nil {
		s.renderError(w, r, "repos", err)
		return
	}
	view := reposView{}
	for _, repo := range repos {
		view.Repos = append(view.Repos, reposViewRow{
			RepoId:        string(repo.RepoId),
			Owner:         repo.Owner,
			Name:          repo.Name,
			DefaultBranch: repo.DefaultBranch,
			Enabled:       repo.Enabled,
		})
	}
	s.renderWith(w, r, "repos", &view)
}

func (s *Server) handleReposTogglePOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, "repos", err)
		return
	}
	repoId := ports.RepoId(r.PostFormValue("repo_id"))
	want := r.PostFormValue("enabled") == "true"
	if err := s.deps.Repos.SetEnabled(r.Context(), repoId, want); err != nil {
		s.renderError(w, r, "repos", err)
		return
	}
	if !want {
		// Tombstone per design §15: clear code_chunks + review_comments
		// for this repo so retrieval no longer surfaces them. pr_runs
		// stays for audit; the janitor retention applies later.
		if err := s.deps.Repos.Tombstone(r.Context(), repoId); err != nil {
			s.deps.Obs.Logger.Warn("tombstone failed", "repo_id", string(repoId), "err", err.Error())
		}
	}
	http.Redirect(w, r, "/repos", http.StatusFound)
}
