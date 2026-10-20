package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const (
	requirementIdentityAuthenticated = "authenticated"
	requirementIdentityLoggedOut     = "logged-out"
	requirementIdentityUnavailable   = "unavailable"
	requirementIdentityTokenPrefix   = "reqid-v1:"
)

type requirementIdentitySession struct {
	context    string
	raw        string
	token      string
	state      string
	generation uint64
}

type requirementIdentityRequest struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	contextKey string
	generation uint64
	consumers  int
	finished   bool
	session    requirementIdentitySession
	loggedOut  bool
	err        error
}

type RequirementIdentityChangedError struct {
	State    string
	Identity string
}

func (e *RequirementIdentityChangedError) Error() string {
	if e != nil && e.State == requirementIdentityLoggedOut {
		return "Meegle 当前已退出登录，请重新确认身份后读取需求"
	}
	return "Meegle 登录身份已变化，请重新读取需求"
}

// RequirementIdentity reads the active CLI identity with the same profile,
// host, user and tenant semantics used by requirement-cache isolation. The
// returned identity is a stable opaque digest; command output and the raw
// identity are never included in the response.
func (s *Service) RequirementIdentity(ctx context.Context, config domain.AppConfig) (state, identity string, err error) {
	if s == nil {
		return requirementIdentityUnavailable, "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	session, loggedOut, probeErr := s.requirementIdentityFor(ctx, config, "", true, s.requirementCacheIdentity)
	if process.IsCleanupFailure(probeErr) {
		return "", "", probeErr
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if probeErr != nil {
		return requirementIdentityUnavailable, "", nil
	}
	if loggedOut {
		return requirementIdentityLoggedOut, "", nil
	}
	return requirementIdentityAuthenticated, session.token, nil
}

func (s *Service) beginRequirementIdentityProbe() uint64 {
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	s.identityProbe++
	return s.identityProbe
}

func (s *Service) recordRequirementIdentityProbe(probe uint64, state, identity string) {
	s.recordRequirementIdentityProbeContext(probe, "", "", state, identity, 0)
}

func (s *Service) recordRequirementIdentityProbeContext(probe uint64, contextKey, raw, state, identity string, generation uint64) {
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if probe != s.identityProbe {
		return
	}
	switch state {
	case requirementIdentityAuthenticated:
		if identity == "" {
			return
		}
		s.identityConfirmed = true
		s.identityState = state
		s.confirmedIdentity = identity
		s.confirmedIdentityRaw = raw
		s.identityContext = contextKey
		if generation > 0 {
			s.identitySessionGeneration = generation
		}
	case requirementIdentityLoggedOut:
		s.identityConfirmed = true
		s.identityState = state
		s.confirmedIdentity = ""
		s.confirmedIdentityRaw = ""
		s.identityContext = contextKey
		if generation > 0 {
			s.identitySessionGeneration = generation
		}
		// An unavailable non-strict probe preserves the last confirmed session.
	}
}

func (s *Service) requirementIdentityContext(config domain.AppConfig) string {
	return s.requirementIdentityContextWithContext(context.Background(), config)
}
func (s *Service) requirementIdentityContextWithContext(ctx context.Context, config domain.AppConfig) string {
	home := filepath.Clean(s.paths.Home)
	if absolute, err := filepath.Abs(home); err == nil {
		home = absolute
	}
	cli := s.commandPath(ctx, config, domain.ProxyTargetMeegle)
	if absolute, err := filepath.Abs(cli); err == nil && cli != "" {
		cli = absolute
	}
	encoded, _ := marshalRequirementJSON([]any{
		filepath.ToSlash(strings.ToLower(home)), filepath.ToSlash(strings.ToLower(cli)),
		config.MeegleExecutablePath, config.MeegleProjects,
		config.CommandProxyURL, config.CommandProxyNoProxy, config.CommandProxyUsername,
		config.CommandProxyPassword, config.CommandProxyTargets,
	})
	return requirementCacheDigest(string(encoded))
}

// requirementIdentityFor reuses an authenticated identity only for the same
// local configuration context. Forced refreshes always probe; simultaneous
// probes for one context share a cancellable request with independent readers.
func (s *Service) requirementIdentityFor(ctx context.Context, config domain.AppConfig, expected string, force bool,
	probeIdentity func(context.Context, domain.AppConfig) (string, bool, error)) (requirementIdentitySession, bool, error) {
	if s == nil {
		return requirementIdentitySession{}, false, errors.New("Meegle 身份服务不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return requirementIdentitySession{}, false, err
	}
	if expected != "" && !validOpaqueRequirementIdentity(expected) {
		return requirementIdentitySession{}, false, errors.New("需求读取预期账户身份无效")
	}
	s.identityStateMu.Lock()
	closed := s.identityClosed
	s.identityStateMu.Unlock()
	if closed {
		return requirementIdentitySession{}, false, errors.New("需求身份读取服务正在关闭")
	}
	contextKey := s.requirementIdentityContextWithContext(ctx, config)
	if err := ctx.Err(); err != nil {
		return requirementIdentitySession{}, false, err
	}
	s.identityStateMu.Lock()
	if s.identityClosed {
		s.identityStateMu.Unlock()
		return requirementIdentitySession{}, false, errors.New("需求身份读取服务正在关闭")
	}
	if s.identityContext != "" && s.identityContext != contextKey {
		// A changed app home, CLI, profile-related config or proxy scope starts a
		// new identity session. Drop prior sessions so returning to an old config
		// cannot silently reuse an identity captured before the context switch.
		s.identityEpochCounter++
		s.identitySessions = make(map[string]requirementIdentitySession)
		s.identityConfirmed = false
		s.identityState = requirementIdentityUnavailable
		s.confirmedIdentity = ""
		s.confirmedIdentityRaw = ""
		s.identitySessionGeneration = 0
		s.identityContext = contextKey
		for requestContext, request := range s.identityRequests {
			if requestContext != contextKey {
				request.cancel()
			}
		}
	} else if s.identityContext == "" {
		s.identityContext = contextKey
	}
	s.identityStateMu.Unlock()
	if !force {
		s.identityStateMu.Lock()
		session, confirmed := s.identitySessions[contextKey]
		if !confirmed && s.identityConfirmed && s.identityContext == contextKey {
			session = requirementIdentitySession{context: s.identityContext, raw: s.confirmedIdentityRaw,
				token: s.confirmedIdentity, state: s.identityState, generation: s.identitySessionGeneration}
			confirmed = true
		}
		s.identityStateMu.Unlock()
		if confirmed {
			return checkRequirementIdentitySession(session, expected)
		}
	}
	if probeIdentity == nil {
		return requirementIdentitySession{}, false, errors.New("Meegle 身份探测未配置")
	}
	if err := ctx.Err(); err != nil {
		return requirementIdentitySession{}, false, err
	}
	s.identityStateMu.Lock()
	if s.identityRequests == nil {
		s.identityRequests = make(map[string]*requirementIdentityRequest)
	}
	request := s.identityRequests[contextKey]
	if request == nil || request.finished || request.ctx.Err() != nil {
		s.identityProbe++
		shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
		request = &requirementIdentityRequest{ctx: shared, cancel: cancel, done: make(chan struct{}),
			contextKey: contextKey, generation: s.identityProbe}
		s.identityRequests[contextKey] = request
		go s.runRequirementIdentityProbe(request, config, probeIdentity)
	}
	request.consumers++
	s.identityStateMu.Unlock()
	select {
	case <-ctx.Done():
		if s.releaseRequirementIdentityRequest(request) {
			<-request.done
			if process.IsCleanupFailure(request.err) {
				return requirementIdentitySession{}, false, request.err
			}
		}
		return requirementIdentitySession{}, false, ctx.Err()
	case <-request.done:
		session, loggedOut, err := request.session, request.loggedOut, request.err
		s.releaseRequirementIdentityRequest(request)
		if process.IsCleanupFailure(err) {
			return requirementIdentitySession{}, false, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return requirementIdentitySession{}, false, ctxErr
		}
		if err != nil {
			return requirementIdentitySession{}, false, err
		}
		return checkRequirementIdentitySession(session, expected, loggedOut)
	}
}

func checkRequirementIdentitySession(session requirementIdentitySession, expected string, loggedOutValue ...bool) (requirementIdentitySession, bool, error) {
	loggedOut := session.state == requirementIdentityLoggedOut
	if len(loggedOutValue) > 0 {
		loggedOut = loggedOutValue[0]
	}
	if loggedOut {
		if expected != "" {
			return requirementIdentitySession{}, true, &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
		}
		return session, true, nil
	}
	if session.state != requirementIdentityAuthenticated || session.raw == "" || session.token == "" {
		return requirementIdentitySession{}, false, errors.New("无法确认当前 Meegle 账户，请重试")
	}
	if expected != "" && session.token != expected {
		return requirementIdentitySession{}, false, &RequirementIdentityChangedError{State: requirementIdentityAuthenticated, Identity: session.token}
	}
	return session, false, nil
}

func (s *Service) runRequirementIdentityProbe(request *requirementIdentityRequest, config domain.AppConfig,
	probeIdentity func(context.Context, domain.AppConfig) (string, bool, error)) {
	defer func() {
		s.identityStateMu.Lock()
		request.finished = true
		if s.identityRequests[request.contextKey] == request {
			delete(s.identityRequests, request.contextKey)
		}
		close(request.done)
		s.identityStateMu.Unlock()
		request.cancel()
	}()
	raw, loggedOut, err := probeIdentity(request.ctx, config)
	if request.ctx.Err() != nil && !process.IsCleanupFailure(err) {
		err = request.ctx.Err()
	}
	state, token, tokenErr := s.requirementIdentityFromProbe(raw, loggedOut, err)
	if tokenErr != nil {
		err = tokenErr
	}
	request.session = requirementIdentitySession{context: request.contextKey, raw: raw, token: token,
		state: state}
	request.loggedOut = loggedOut
	request.err = err
	if err != nil || state == requirementIdentityUnavailable {
		return
	}
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if s.identityRequests[request.contextKey] != request || s.identityContext != request.contextKey {
		request.err = &RequirementIdentityChangedError{State: state, Identity: token}
		return
	}
	if s.identitySessions == nil {
		s.identitySessions = make(map[string]requirementIdentitySession)
	}
	previous, hadPrevious := s.identitySessions[request.contextKey]
	if !hadPrevious && s.identityConfirmed && s.identityContext == request.contextKey {
		previous = requirementIdentitySession{context: s.identityContext, raw: s.confirmedIdentityRaw,
			token: s.confirmedIdentity, state: s.identityState, generation: s.identitySessionGeneration}
		hadPrevious = true
	}
	if hadPrevious && previous.context == request.contextKey && previous.raw == raw && previous.token == token && previous.state == state && previous.generation > 0 {
		request.session.generation = previous.generation
	} else {
		s.identityEpochCounter++
		if s.identityEpochCounter == 0 {
			s.identityEpochCounter++
		}
		request.session.generation = s.identityEpochCounter
	}
	s.identitySessions[request.contextKey] = request.session
	// Keep the legacy single-account snapshot for list/cache flows. Its latest
	// probe wins, while requirement reads retain independent per-context sessions.
	if request.generation == s.identityProbe {
		s.identityConfirmed = true
		s.identityState = state
		s.confirmedIdentity = token
		s.confirmedIdentityRaw = raw
		s.identityContext = request.contextKey
		s.identitySessionGeneration = request.session.generation
	}
}

func (s *Service) releaseRequirementIdentityRequest(request *requirementIdentityRequest) bool {
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	request.consumers--
	if request.consumers == 0 && !request.finished {
		request.cancel()
	}
	return request.consumers == 0
}

// CloseRequirementIdentityRequests retires identity probes when the desktop
// service shuts down. A request is canceled once, shared by all subscribers,
// and joined so the runner can finish its process-tree cleanup before exit.
func (s *Service) CloseRequirementIdentityRequests(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.identityStateMu.Lock()
	s.identityClosed = true
	requests := make([]*requirementIdentityRequest, 0, len(s.identityRequests))
	for _, request := range s.identityRequests {
		requests = append(requests, request)
		request.cancel()
	}
	s.identityStateMu.Unlock()
	for _, request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Service) verifyRequirementIdentitySession(ctx context.Context, config domain.AppConfig, expected string, generation uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	contextKey := s.requirementIdentityContextWithContext(ctx, config)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.identityStateMu.Lock()
	session, found := s.identitySessions[contextKey]
	if !found && s.identityConfirmed && s.identityContext == contextKey {
		session = requirementIdentitySession{context: s.identityContext, raw: s.confirmedIdentityRaw,
			token: s.confirmedIdentity, state: s.identityState, generation: s.identitySessionGeneration}
		found = true
	}
	valid := found && session.context == contextKey && generation > 0 && session.generation == generation
	s.identityStateMu.Unlock()
	if !valid {
		if session.state == requirementIdentityLoggedOut {
			return &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
		}
		return &RequirementIdentityChangedError{State: session.state, Identity: session.token}
	}
	if session.state == requirementIdentityLoggedOut {
		return &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
	}
	if _, _, err := checkRequirementIdentitySession(session, ""); err != nil {
		return err
	}
	if expected != "" && session.raw != expected {
		return &RequirementIdentityChangedError{State: requirementIdentityAuthenticated, Identity: session.token}
	}
	return ctx.Err()
}

func (s *Service) confirmedRequirementIdentity(expected string) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if !s.identityConfirmed {
		return false, nil
	}
	if s.identityState == requirementIdentityLoggedOut {
		return false, &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
	}
	if s.identityState == requirementIdentityAuthenticated && s.confirmedIdentity == expected {
		return true, nil
	}
	return false, &RequirementIdentityChangedError{State: s.identityState, Identity: s.confirmedIdentity}
}

func classifyRequirementIdentityProbe(raw string, loggedOut bool, probeErr error) string {
	if loggedOut {
		return requirementIdentityLoggedOut
	}
	if probeErr != nil || strings.TrimSpace(raw) == "" {
		return requirementIdentityUnavailable
	}
	return requirementIdentityAuthenticated
}

func (s *Service) requirementIdentityFromProbe(raw string, loggedOut bool, probeErr error) (string, string, error) {
	state := classifyRequirementIdentityProbe(raw, loggedOut, probeErr)
	if state != requirementIdentityAuthenticated {
		return state, "", nil
	}
	return requirementIdentityAuthenticated, opaqueRequirementIdentity(raw), nil
}

func opaqueRequirementIdentity(raw string) string {
	const identityDomain = "silverwing-meegle-requirement-identity-v1\x00"
	digest := sha256.Sum256([]byte(identityDomain + raw))
	return requirementIdentityTokenPrefix + hex.EncodeToString(digest[:])
}

func validOpaqueRequirementIdentity(identity string) bool {
	if !strings.HasPrefix(identity, requirementIdentityTokenPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(identity, requirementIdentityTokenPrefix)
	if len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func matchesOpaqueRequirementIdentity(raw, expected string) bool {
	return validOpaqueRequirementIdentity(expected) && opaqueRequirementIdentity(raw) == expected
}

func expectedRequirementIdentity(payload map[string]any) (string, bool, error) {
	value, present := payload["expectedIdentity"]
	if !present {
		return "", false, nil
	}
	identity, ok := value.(string)
	identity = strings.TrimSpace(identity)
	if !ok || !validOpaqueRequirementIdentity(identity) {
		return "", true, errors.New("需求列表身份标记无效，请重新读取 Meegle 身份")
	}
	return identity, true, nil
}

type requirementIdentityVerifier func(context.Context, string) error
type requirementListExecutor func(context.Context) (any, error)

func executeRequirementListWithIdentity(
	ctx context.Context,
	expected string,
	verify requirementIdentityVerifier,
	execute requirementListExecutor,
) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == "" {
		if execute == nil {
			return nil, errors.New("需求列表读取未配置")
		}
		return execute(ctx)
	}
	if verify == nil || execute == nil {
		return nil, errors.New("需求列表身份校验未配置")
	}
	if err := verify(ctx, expected); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, operationErr := execute(ctx)
	if process.IsCleanupFailure(operationErr) {
		return nil, operationErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := verify(ctx, expected); err != nil {
		return nil, err
	}
	return result, operationErr
}

func (s *Service) verifyExpectedRequirementIdentity(ctx context.Context, config domain.AppConfig, token string) error {
	if !validOpaqueRequirementIdentity(token) {
		return errors.New("需求列表身份标记无效，请重新读取 Meegle 身份")
	}
	return s.verifyRequirementIdentity(ctx, config, token)
}

func (s *Service) requirementIdentitySnapshot() (state, identity string, confirmed bool) {
	if s == nil {
		return "", "", false
	}
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	return s.identityState, s.confirmedIdentity, s.identityConfirmed
}

func requirementIdentityMatches(actual, expected string) bool {
	if validOpaqueRequirementIdentity(expected) {
		return matchesOpaqueRequirementIdentity(actual, expected)
	}
	return actual == expected
}
