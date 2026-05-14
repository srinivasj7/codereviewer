// Package fakes provides in-memory implementations of the system's
// ports for use in unit and integration tests. They live under
// internal/testing so production code can't accidentally depend on them.
package fakes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"codereviewer/internal/ports"
)

// Vcs is an in-memory VcsSource that records all calls for later
// assertion. Set DiffByRef and the optional *Err fields to control behavior.
type Vcs struct {
	mu               sync.Mutex
	diffByRef        map[string]ports.UnifiedDiff
	prComments       map[string][]ports.HumanComment
	prMeta           map[string]ports.PrMeta
	filesAt          map[string]string
	closedPrs        []int
	postReviewCalls  []PostReviewCall
	updateCheckCalls []UpdateCheckCall
	FetchDiffErr     error
	PostReviewErr    error
}

// PostReviewCall records one PostReview invocation.
type PostReviewCall struct {
	Ref    ports.PrRef
	Review ports.ReviewPayload
}

// UpdateCheckCall records one UpdateCheck invocation.
type UpdateCheckCall struct {
	Ref    ports.PrRef
	Result ports.CheckResult
}

// NewVcs returns an empty Vcs fake.
func NewVcs() *Vcs {
	return &Vcs{diffByRef: make(map[string]ports.UnifiedDiff)}
}

// SetDiff stores the diff returned by FetchDiff for ref.
func (v *Vcs) SetDiff(ref ports.PrRef, diff ports.UnifiedDiff) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.diffByRef[diffKey(ref)] = diff
}

// PostReviews returns a snapshot of recorded PostReview calls.
func (v *Vcs) PostReviews() []PostReviewCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]PostReviewCall, len(v.postReviewCalls))
	copy(out, v.postReviewCalls)
	return out
}

// UpdateChecks returns a snapshot of recorded UpdateCheck calls.
func (v *Vcs) UpdateChecks() []UpdateCheckCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]UpdateCheckCall, len(v.updateCheckCalls))
	copy(out, v.updateCheckCalls)
	return out
}

// VerifyWebhook is not implemented for the fake.
func (v *Vcs) VerifyWebhook(_ context.Context, _ http.Header, _ []byte) (ports.WebhookEvent, error) {
	return ports.WebhookEvent{}, errors.New("fake vcs: VerifyWebhook not implemented")
}

// FetchDiff returns the diff previously installed via SetDiff.
func (v *Vcs) FetchDiff(_ context.Context, ref ports.PrRef) (ports.UnifiedDiff, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.FetchDiffErr != nil {
		return ports.UnifiedDiff{}, v.FetchDiffErr
	}
	diff, ok := v.diffByRef[diffKey(ref)]
	if !ok {
		return ports.UnifiedDiff{}, fmt.Errorf("fake vcs: no diff for %s/%d@%s", ref.RepoId, ref.PrNumber, ref.HeadSha)
	}
	return diff, nil
}

// FetchFileAt returns the content installed via SetFileAt, or empty
// string + an error when no content is registered (some providers
// treat the empty-string return as "absent").
func (v *Vcs) FetchFileAt(_ context.Context, repoId ports.RepoId, sha, path string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := string(repoId) + "@" + sha + ":" + path
	if c, ok := v.filesAt[key]; ok {
		return c, nil
	}
	return "", errors.New("fake vcs: no file registered for " + key)
}

// SetFileAt installs the content returned by FetchFileAt.
func (v *Vcs) SetFileAt(repoId ports.RepoId, sha, path, content string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.filesAt == nil {
		v.filesAt = make(map[string]string)
	}
	v.filesAt[string(repoId)+"@"+sha+":"+path] = content
}

// FetchPrMeta returns the meta installed via SetPrMeta, or zero-value.
func (v *Vcs) FetchPrMeta(_ context.Context, ref ports.PrRef) (ports.PrMeta, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if m, ok := v.prMeta[diffKey(ref)]; ok {
		return m, nil
	}
	return ports.PrMeta{}, nil
}

// SetPrMeta installs the meta returned by FetchPrMeta for ref.
func (v *Vcs) SetPrMeta(ref ports.PrRef, meta ports.PrMeta) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prMeta == nil {
		v.prMeta = make(map[string]ports.PrMeta)
	}
	v.prMeta[diffKey(ref)] = meta
}

// ListChangedFiles returns an empty list.
func (v *Vcs) ListChangedFiles(_ context.Context, _ ports.RepoId, _, _ string) ([]ports.ChangedFile, error) {
	return nil, nil
}

// ListPrComments returns the comments installed via SetPrComments,
// or empty if none.
func (v *Vcs) ListPrComments(_ context.Context, ref ports.PrRef) ([]ports.HumanComment, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.prComments[diffKey(ref)], nil
}

// SetPrComments installs comments returned by ListPrComments for ref.
func (v *Vcs) SetPrComments(ref ports.PrRef, comments []ports.HumanComment) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prComments == nil {
		v.prComments = make(map[string][]ports.HumanComment)
	}
	v.prComments[diffKey(ref)] = comments
}

// SearchClosedPrs returns PR numbers installed via SetClosedPrs.
func (v *Vcs) SearchClosedPrs(_ context.Context, _ ports.RepoId, _ time.Time) ([]int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]int, len(v.closedPrs))
	copy(out, v.closedPrs)
	return out, nil
}

// SetClosedPrs installs the PR numbers returned by SearchClosedPrs.
func (v *Vcs) SetClosedPrs(numbers []int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closedPrs = append([]int(nil), numbers...)
}

// PostReview records the call and synthesizes a PostedReview.
func (v *Vcs) PostReview(_ context.Context, ref ports.PrRef, review ports.ReviewPayload) (ports.PostedReview, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.PostReviewErr != nil {
		return ports.PostedReview{}, v.PostReviewErr
	}
	v.postReviewCalls = append(v.postReviewCalls, PostReviewCall{Ref: ref, Review: review})
	id := int64(len(v.postReviewCalls))
	commentIds := make([]int64, len(review.Comments))
	for i := range commentIds {
		commentIds[i] = id*1000 + int64(i)
	}
	return ports.PostedReview{
		ReviewId:   id,
		PostedAt:   time.Now(),
		CommentIds: commentIds,
	}, nil
}

// UpdateCheck records the call.
func (v *Vcs) UpdateCheck(_ context.Context, ref ports.PrRef, result ports.CheckResult) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.updateCheckCalls = append(v.updateCheckCalls, UpdateCheckCall{Ref: ref, Result: result})
	return nil
}

func diffKey(ref ports.PrRef) string {
	return fmt.Sprintf("%s:%d:%s", ref.RepoId, ref.PrNumber, ref.HeadSha)
}
