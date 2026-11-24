// Package policy is the deterministic authorization engine. It has no I/O.
// The store calls into it while holding the budget locks so that the decision
// and the reservation are atomic.
package policy

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
)

// Code identifies the reason a request was denied. Codes are stable API values.
type Code string

const (
	CodeOrgPaused            Code = "org_paused"
	CodeAgentRevoked         Code = "agent_revoked"
	CodeTaskClosed           Code = "task_closed"
	CodeTaskDeadline         Code = "task_deadline_passed"
	CodeTaskMismatch         Code = "task_not_owned_by_agent"
	CodeURLInvalid           Code = "url_invalid"
	CodeEndpointNotAllowed   Code = "endpoint_not_allowed"
	CodeMethodNotAllowed     Code = "method_not_allowed"
	CodeRecipientNotApproved Code = "recipient_not_approved"
	CodeWrongAsset           Code = "wrong_asset"
	CodeWrongNetwork         Code = "wrong_network"
	CodeUnsupportedScheme    Code = "unsupported_scheme"
	CodeResourceMismatch     Code = "resource_mismatch"
	CodePerRequestLimit      Code = "per_request_limit_exceeded"
	CodeTaskBudget           Code = "task_budget_exceeded"
	CodeAgentDaily           Code = "agent_daily_limit_exceeded"
	CodeOrgDaily             Code = "org_daily_limit_exceeded"
	CodeInsufficientBalance  Code = "insufficient_balance"
	CodeAmountOverflow       Code = "amount_overflow"
	CodeQuoteTimeout         Code = "quote_timeout_invalid"
)

// Denial is returned when policy rejects a request.
type Denial struct {
	Code   Code
	Detail string
}

func (d *Denial) Error() string { return fmt.Sprintf("policy denied: %s (%s)", d.Code, d.Detail) }

func deny(c Code, format string, args ...any) error {
	return &Denial{Code: c, Detail: fmt.Sprintf(format, args...)}
}

// AsDenial extracts a Denial from err.
func AsDenial(err error) (*Denial, bool) {
	var d *Denial
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// Usage is the budget consumption the store computed under lock.
type Usage struct {
	OrgAvailable   money.Amount // spendable balance excluding open reservations
	OrgDailyUsed   money.Amount // held + committed today (UTC)
	AgentDailyUsed money.Amount
	TaskUsed       money.Amount // held + committed for the task
}

// Principals bundles the state checked on every request.
type Principals struct {
	Org   domain.Organization
	Agent domain.AgentPrincipal
	Task  domain.Task
}

// CheckPrincipals rejects paused orgs, revoked agents, closed/expired tasks,
// and tasks that do not belong to the agent.
func CheckPrincipals(p Principals, now time.Time) error {
	if p.Org.Status != domain.OrgActive {
		return deny(CodeOrgPaused, "organization %s is %s", p.Org.ID, p.Org.Status)
	}
	if p.Agent.Status != domain.AgentActive {
		return deny(CodeAgentRevoked, "agent %s is %s", p.Agent.ID, p.Agent.Status)
	}
	if p.Task.AgentID != p.Agent.ID || p.Task.OrgID != p.Org.ID {
		return deny(CodeTaskMismatch, "task %s does not belong to agent %s", p.Task.ID, p.Agent.ID)
	}
	if p.Task.Status != domain.TaskActive {
		return deny(CodeTaskClosed, "task %s is %s", p.Task.ID, p.Task.Status)
	}
	if !now.Before(p.Task.Deadline) {
		return deny(CodeTaskDeadline, "task %s deadline %s passed", p.Task.ID, p.Task.Deadline.UTC().Format(time.RFC3339))
	}
	return nil
}

// NormalizeURL parses and canonicalizes an absolute resource URL. It rejects
// userinfo, fragments, empty hosts, and non-http(s) schemes.
func NormalizeURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, deny(CodeURLInvalid, "%v", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, deny(CodeURLInvalid, "url must be absolute with a host")
	}
	if u.User != nil {
		return nil, deny(CodeURLInvalid, "userinfo not permitted")
	}
	if u.Fragment != "" {
		return nil, deny(CodeURLInvalid, "fragment not permitted")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, deny(CodeURLInvalid, "scheme %q not permitted", u.Scheme)
	}
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// MatchEndpoint returns the first rule allowing method+url, or a Denial.
func MatchEndpoint(p domain.BudgetPolicyVersion, method, raw string) (*domain.EndpointRule, error) {
	u, err := NormalizeURL(raw)
	if err != nil {
		return nil, err
	}
	method = strings.ToUpper(method)
	for i := range p.Endpoints {
		rule := &p.Endpoints[i]
		if !prefixMatches(rule.URLPrefix, u) {
			continue
		}
		if !methodAllowed(rule, method) {
			return nil, deny(CodeMethodNotAllowed, "method %s not allowed for %s", method, rule.URLPrefix)
		}
		return rule, nil
	}
	return nil, deny(CodeEndpointNotAllowed, "%s is not an approved endpoint", u.String())
}

func methodAllowed(rule *domain.EndpointRule, method string) bool {
	if len(rule.Methods) == 0 {
		return method == "GET"
	}
	for _, m := range rule.Methods {
		if strings.ToUpper(m) == method {
			return true
		}
	}
	return false
}

// prefixMatches requires equal scheme and host and a path prefix that ends on
// a segment boundary, so "/api/v1" matches "/api/v1/x" but not "/api/v1x".
func prefixMatches(prefix string, u *url.URL) bool {
	p, err := url.Parse(prefix)
	if err != nil || !p.IsAbs() {
		return false
	}
	if strings.ToLower(p.Scheme) != u.Scheme || strings.ToLower(p.Host) != u.Host {
		return false
	}
	pp := p.Path
	if pp == "" {
		pp = "/"
	}
	path := u.Path
	if pp == "/" {
		return true
	}
	pp = strings.TrimSuffix(pp, "/")
	return path == pp || strings.HasPrefix(path, pp+"/")
}

// ValidateQuote checks the resource server's payment requirement against the
// policy's pinned asset and the matched endpoint rule's approved recipients.
func ValidateQuote(p domain.BudgetPolicyVersion, rule *domain.EndpointRule, requestURL string, q domain.ResourceQuote) error {
	if !strings.EqualFold(q.Scheme, p.Asset.Scheme) {
		return deny(CodeUnsupportedScheme, "scheme %q not supported (policy allows %q)", q.Scheme, p.Asset.Scheme)
	}
	if !strings.EqualFold(q.Network, p.Asset.Network) {
		return deny(CodeWrongNetwork, "network %q rejected (policy allows %q)", q.Network, p.Asset.Network)
	}
	if !strings.EqualFold(q.Asset, p.Asset.Asset) {
		return deny(CodeWrongAsset, "asset %q rejected (policy allows %q)", q.Asset, p.Asset.Asset)
	}
	if !recipientApproved(rule, q.PayTo) {
		return deny(CodeRecipientNotApproved, "beneficiary %q is not approved for %s", q.PayTo, rule.URLPrefix)
	}
	if q.MaxTimeoutSeconds < 0 || q.MaxTimeoutSeconds > 3600 {
		return deny(CodeQuoteTimeout, "maxTimeoutSeconds %d out of range", q.MaxTimeoutSeconds)
	}
	if q.Resource != "" {
		ru, err := NormalizeURL(q.Resource)
		if err != nil {
			return deny(CodeResourceMismatch, "quote resource invalid: %v", err)
		}
		if !prefixMatches(rule.URLPrefix, ru) {
			return deny(CodeResourceMismatch, "quote resource %s is outside approved endpoint %s", q.Resource, rule.URLPrefix)
		}
	}
	if q.MaxAmountRequired.Cmp(p.PerRequestMax) > 0 {
		return deny(CodePerRequestLimit, "amount %s exceeds per-request max %s", q.MaxAmountRequired, p.PerRequestMax)
	}
	return nil
}

func recipientApproved(rule *domain.EndpointRule, payTo string) bool {
	if payTo == "" {
		return false
	}
	for _, r := range rule.Recipients {
		if strings.EqualFold(strings.TrimSpace(r), strings.TrimSpace(payTo)) {
			return true
		}
	}
	return false
}

// CheckBudgets verifies that amount+fee fits in every applicable budget given
// the usage computed under lock. It returns the total to hold.
func CheckBudgets(p domain.BudgetPolicyVersion, task domain.Task, usage Usage, amount, fee money.Amount) (money.Amount, error) {
	total, err := amount.Add(fee)
	if err != nil {
		return money.Zero(), deny(CodeAmountOverflow, "%v", err)
	}
	if amount.Cmp(p.PerRequestMax) > 0 {
		return money.Zero(), deny(CodePerRequestLimit, "amount %s exceeds per-request max %s", amount, p.PerRequestMax)
	}
	taskCap := money.Min(task.Budget, p.PerTaskMax)
	if exceeds(usage.TaskUsed, total, taskCap) {
		return money.Zero(), deny(CodeTaskBudget, "task used %s + %s exceeds task budget %s", usage.TaskUsed, total, taskCap)
	}
	if exceeds(usage.AgentDailyUsed, total, p.AgentDailyMax) {
		return money.Zero(), deny(CodeAgentDaily, "agent daily used %s + %s exceeds %s", usage.AgentDailyUsed, total, p.AgentDailyMax)
	}
	if exceeds(usage.OrgDailyUsed, total, p.OrgDailyMax) {
		return money.Zero(), deny(CodeOrgDaily, "organization daily used %s + %s exceeds %s", usage.OrgDailyUsed, total, p.OrgDailyMax)
	}
	if usage.OrgAvailable.Cmp(total) < 0 {
		return money.Zero(), deny(CodeInsufficientBalance, "available %s is below required hold %s", usage.OrgAvailable, total)
	}
	return total, nil
}

// exceeds reports whether used+add > limit, treating overflow as exceeding.
func exceeds(used, add, limit money.Amount) bool {
	sum, err := used.Add(add)
	if err != nil {
		return true
	}
	return sum.Cmp(limit) > 0
}

// ValidatePolicy enforces structural rules on an owner-submitted policy.
func ValidatePolicy(p domain.BudgetPolicyVersion) error {
	if p.Asset.Asset == "" || p.Asset.Network == "" || p.Asset.Scheme == "" {
		return errors.New("policy asset, network and scheme are required")
	}
	if p.PerRequestMax.Cmp(p.PerTaskMax) > 0 {
		return errors.New("per_request_max cannot exceed per_task_max")
	}
	if p.PerTaskMax.Cmp(p.AgentDailyMax) > 0 {
		return errors.New("per_task_max cannot exceed agent_daily_max")
	}
	if p.AgentDailyMax.Cmp(p.OrgDailyMax) > 0 {
		return errors.New("agent_daily_max cannot exceed org_daily_max")
	}
	if len(p.Endpoints) == 0 {
		return errors.New("at least one endpoint rule is required")
	}
	for _, e := range p.Endpoints {
		u, err := url.Parse(e.URLPrefix)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("endpoint url_prefix %q must be an absolute URL without query, fragment or userinfo", e.URLPrefix)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("endpoint url_prefix %q must use http(s)", e.URLPrefix)
		}
		if len(e.Recipients) == 0 {
			return fmt.Errorf("endpoint %q requires at least one approved recipient", e.URLPrefix)
		}
		for _, r := range e.Recipients {
			if strings.TrimSpace(r) == "" {
				return fmt.Errorf("endpoint %q has an empty recipient", e.URLPrefix)
			}
		}
	}
	if p.GrantTTLSeconds <= 0 || p.GrantTTLSeconds > 300 {
		return errors.New("grant_ttl_seconds must be between 1 and 300")
	}
	if p.MaxTaskSeconds <= 0 || p.MaxTaskSeconds > 30*24*3600 {
		return errors.New("max_task_seconds must be between 1 and 30 days")
	}
	return nil
}
