package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// instructionRow flattens an InstructionSet for template rendering,
// adding the list of repo IDs assigned to it.
type instructionRow struct {
	SetId         string
	Name          string
	Body          string
	UpdatedAt     string
	UpdatedBy     string
	AssignedRepos []string
}

// repoAssignmentRow is one row of the repo→set assignment table.
type repoAssignmentRow struct {
	RepoId        string
	AssignedSetId string
}

// instructionsView is the full template payload.
type instructionsView struct {
	viewData
	InstructionSets []instructionRow
	Repos           []repoAssignmentRow
}

// handleInstructionsGET renders the instructions page.
func (s *Server) handleInstructionsGET(w http.ResponseWriter, r *http.Request) {
	s.renderInstructions(w, r, "", "")
}

func (s *Server) renderInstructions(w http.ResponseWriter, r *http.Request, okMsg, errMsg string) {
	tenant := ports.TenantId(s.deps.Cfg.Tenant.Id)
	sets, err := s.deps.Context.ListInstructionSets(r.Context(), tenant)
	if err != nil {
		s.renderError(w, r, "instructions", fmt.Errorf("list sets: %w", err))
		return
	}

	// Walk repos to discover assignments per set.
	var allRepos []ports.RepoRef
	if s.deps.Repos != nil {
		allRepos, _ = s.deps.Repos.ListByTenant(r.Context(), tenant)
	}
	assignedFor := make(map[string][]string)
	repoCurrent := make(map[string]string)
	for _, repo := range allRepos {
		set, found, _ := s.deps.Context.GetSetForRepo(r.Context(), repo.RepoId)
		if found {
			assignedFor[set.SetId] = append(assignedFor[set.SetId], string(repo.RepoId))
			repoCurrent[string(repo.RepoId)] = set.SetId
		}
	}

	rows := make([]instructionRow, 0, len(sets))
	for _, set := range sets {
		rows = append(rows, instructionRow{
			SetId:         set.SetId,
			Name:          set.Name,
			Body:          set.Body,
			UpdatedAt:     set.UpdatedAt.Format(time.RFC3339),
			UpdatedBy:     set.UpdatedBy,
			AssignedRepos: assignedFor[set.SetId],
		})
	}
	repoRows := make([]repoAssignmentRow, 0, len(allRepos))
	for _, repo := range allRepos {
		repoRows = append(repoRows, repoAssignmentRow{
			RepoId:        string(repo.RepoId),
			AssignedSetId: repoCurrent[string(repo.RepoId)],
		})
	}

	vd := instructionsView{
		viewData:        viewData{FlashOk: okMsg, FlashErr: errMsg},
		InstructionSets: rows,
		Repos:           repoRows,
	}
	s.renderWith(w, r, "instructions", &vd)
}

// handleInstructionsPOST handles upsert / delete / assign actions.
func (s *Server) handleInstructionsPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, "instructions", err)
		return
	}
	action := r.PostFormValue("action")
	sess, _ := readSession(r, s.secret)
	tenant := ports.TenantId(s.deps.Cfg.Tenant.Id)

	switch action {
	case "upsert":
		set := store.InstructionSet{
			SetId:     r.PostFormValue("set_id"),
			TenantId:  tenant,
			Name:      strings.TrimSpace(r.PostFormValue("name")),
			Body:      r.PostFormValue("body"),
			UpdatedBy: sess.Subject,
		}
		if set.Name == "" {
			s.renderInstructions(w, r, "", "name is required")
			return
		}
		if err := s.deps.Context.UpsertInstructionSet(r.Context(), set); err != nil {
			s.renderInstructions(w, r, "", err.Error())
			return
		}
		s.renderInstructions(w, r, "Set saved.", "")
	case "delete":
		if err := s.deps.Context.DeleteInstructionSet(r.Context(), r.PostFormValue("set_id")); err != nil {
			s.renderInstructions(w, r, "", err.Error())
			return
		}
		s.renderInstructions(w, r, "Set deleted.", "")
	case "assign":
		repoId := ports.RepoId(r.PostFormValue("repo_id"))
		setId := r.PostFormValue("set_id")
		var err error
		if setId == "" {
			err = s.deps.Context.UnassignFromRepo(r.Context(), repoId)
		} else {
			err = s.deps.Context.AssignSetToRepo(r.Context(), repoId, setId)
		}
		if err != nil {
			s.renderInstructions(w, r, "", err.Error())
			return
		}
		s.renderInstructions(w, r, "Assignment updated.", "")
	default:
		s.renderInstructions(w, r, "", "unknown action")
	}
}

// prContextView is the template payload.
type prContextView struct {
	viewData
	QueryRepo    string
	QueryPr      int
	HasPr        bool
	Items        []prContextItemRow
	AllowedHosts []string
}

type prContextItemRow struct {
	ItemId    string
	Source    string
	Title     string
	Body      string
	CreatedAt string
	CreatedBy string
}

func (s *Server) handlePrContextGET(w http.ResponseWriter, r *http.Request) {
	s.renderPrContext(w, r, "", "")
}

func (s *Server) renderPrContext(w http.ResponseWriter, r *http.Request, okMsg, errMsg string) {
	q := r.URL.Query()
	repo := strings.TrimSpace(q.Get("repo"))
	prStr := q.Get("pr")
	vd := prContextView{
		viewData:     viewData{FlashOk: okMsg, FlashErr: errMsg},
		QueryRepo:    repo,
		AllowedHosts: s.deps.Cfg.Context.AllowedUrlHosts,
	}
	if repo != "" && prStr != "" {
		if pr, err := strconv.Atoi(prStr); err == nil && pr > 0 {
			vd.QueryPr = pr
			vd.HasPr = true
			items, _ := s.deps.Context.ListPrContext(r.Context(), ports.PrRef{
				TenantId: ports.TenantId(s.deps.Cfg.Tenant.Id),
				RepoId:   ports.RepoId(repo),
				PrNumber: pr,
			})
			for _, it := range items {
				vd.Items = append(vd.Items, prContextItemRow{
					ItemId:    it.ItemId,
					Source:    it.Source,
					Title:     it.Title,
					Body:      it.Body,
					CreatedAt: it.CreatedAt.Format(time.RFC3339),
					CreatedBy: it.CreatedBy,
				})
			}
		}
	}
	s.renderWith(w, r, "pr_context", &vd)
}

func (s *Server) handlePrContextPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(int64(s.deps.Cfg.Context.UrlFetchMaxBytes) + 1<<20); err != nil {
		// Fall back to ParseForm for non-multipart actions.
		_ = r.ParseForm()
	}
	action := r.FormValue("action")
	repo := r.FormValue("repo")
	prStr := r.FormValue("pr")
	pr, _ := strconv.Atoi(prStr)
	if repo == "" || pr <= 0 {
		http.Error(w, "repo/pr required", http.StatusBadRequest)
		return
	}
	sess, _ := readSession(r, s.secret)
	ref := ports.PrRef{
		TenantId: ports.TenantId(s.deps.Cfg.Tenant.Id),
		RepoId:   ports.RepoId(repo),
		PrNumber: pr,
	}

	switch action {
	case "delete":
		if err := s.deps.Context.DeletePrContextItem(r.Context(), r.FormValue("item_id")); err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		s.flashRedirectPr(w, r, repo, pr, "Item deleted.", "")
	case "attach-text":
		body := strings.TrimSpace(r.FormValue("body"))
		if body == "" {
			s.flashRedirectPr(w, r, repo, pr, "", "body is required")
			return
		}
		if err := s.deps.Context.AppendPrContext(r.Context(), store.PrContextItem{
			TenantId:  ref.TenantId, RepoId: ref.RepoId, PrNumber: pr,
			Source: "text", Title: r.FormValue("title"), Body: body, CreatedBy: sess.Subject,
		}); err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		s.flashRedirectPr(w, r, repo, pr, "Text attached.", "")
	case "attach-file":
		f, header, err := r.FormFile("file")
		if err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		defer f.Close()
		// Cap the file read at UrlFetchMaxBytes (same budget).
		body, err := io.ReadAll(io.LimitReader(f, int64(s.deps.Cfg.Context.UrlFetchMaxBytes)))
		if err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		if err := s.deps.Context.AppendPrContext(r.Context(), store.PrContextItem{
			TenantId:  ref.TenantId, RepoId: ref.RepoId, PrNumber: pr,
			Source: "file:" + header.Filename, Title: header.Filename, Body: string(body),
			CreatedBy: sess.Subject,
		}); err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		s.flashRedirectPr(w, r, repo, pr, "File attached.", "")
	case "attach-url":
		raw := strings.TrimSpace(r.FormValue("url"))
		body, host, err := s.fetchUrlForContext(r.Context(), raw)
		if err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		if err := s.deps.Context.AppendPrContext(r.Context(), store.PrContextItem{
			TenantId:  ref.TenantId, RepoId: ref.RepoId, PrNumber: pr,
			Source: "url:" + host, Title: r.FormValue("title"), Body: body, CreatedBy: sess.Subject,
		}); err != nil {
			s.flashRedirectPr(w, r, repo, pr, "", err.Error())
			return
		}
		s.flashRedirectPr(w, r, repo, pr, "URL fetched and attached.", "")
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

// flashRedirectPr re-renders the pr-context page for the same PR with
// a flash message — keeps the URL stable so a refresh doesn't re-POST.
func (s *Server) flashRedirectPr(w http.ResponseWriter, r *http.Request, repo string, pr int, ok, errMsg string) {
	r2 := r.Clone(r.Context())
	q := url.Values{}
	q.Set("repo", repo)
	q.Set("pr", strconv.Itoa(pr))
	r2.URL = &url.URL{Path: "/pr-context", RawQuery: q.Encode()}
	s.renderPrContext(w, r2, ok, errMsg)
}

// fetchUrlForContext checks the URL against the allow-list, GETs it,
// and returns the body (truncated to UrlFetchMaxBytes) plus the host.
func (s *Server) fetchUrlForContext(ctx context.Context, raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", "", fmt.Errorf("only http(s) URLs are supported")
	}
	if !hostAllowed(u.Host, s.deps.Cfg.Context.AllowedUrlHosts) {
		return "", "", fmt.Errorf("host %q is not in [context].allowed_url_hosts", u.Host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", "", fmt.Errorf("upstream http %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(s.deps.Cfg.Context.UrlFetchMaxBytes)))
	if err != nil {
		return "", "", err
	}
	return string(body), u.Host, nil
}

func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	host = strings.TrimSpace(host)
	// Strip port if present.
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), host) {
			return true
		}
	}
	return false
}
