package app

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/lanaccess"
)

const (
	// lanRequestPath is where another device asks for access. It is the only
	// endpoint in the application that answers without a session, because a
	// device asking for access has no session yet -- that is the whole point.
	lanRequestPath = "/api/lan/request"
	// lanRequestPrefix is where a waiting device collects its answer, keyed by
	// the request id it was given. That id is the only thing it has.
	lanRequestPrefix = "/api/lan/request/"
	// lanDevicesPath is the owner's view of the registry.
	lanDevicesPath = "/api/lan/devices"
	// lanDevicePrefix is approval, refusal and forgetting, keyed by device id.
	lanDevicePrefix = "/api/lan/devices/"
	// lanPathPrefix is what the dispatcher matches; the handler then routes
	// internally, because these paths carry an id and cannot be exact literals.
	lanPathPrefix = "/api/lan/"
)

type lanConfig struct {
	store       *lanaccess.Store
	authService *authn.Service
	logger      *slog.Logger
}

// lanHandler serves the local-network access requests.
//
// It sits outside the generated contract and, unusually, has two different
// audiences: devices that hold no session and are asking for one, and the owner
// deciding. Which is which is decided here rather than by the dispatcher.
type lanHandler struct {
	store  *lanaccess.Store
	auth   *authn.Service
	logger *slog.Logger
}

func newLANHandler(cfg lanConfig) (*lanHandler, error) {
	if cfg.store == nil || cfg.authService == nil {
		return nil, errors.New("lan handler requires the access registry and the auth service")
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	return &lanHandler{store: cfg.store, auth: cfg.authService, logger: cfg.logger}, nil
}

func (h *lanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == lanRequestPath:
		h.serveRequest(w, r)
	case strings.HasPrefix(r.URL.Path, lanRequestPrefix):
		h.serveClaim(w, r)
	case r.URL.Path == lanDevicesPath:
		h.serveDevices(w, r)
	case strings.HasPrefix(r.URL.Path, lanDevicePrefix):
		h.serveDevice(w, r)
	default:
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown local-network endpoint.")
	}
}

type lanRequestResponse struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// Message is shown by the requesting device, which is the only place the
	// person waiting for an answer is looking.
	Message string `json:"message"`
}

// serveRequest records a device asking for access.
//
// It is reachable by anything that can reach the port, which is deliberate: a
// device that cannot ask cannot be approved. What protects the machine is that
// asking grants nothing. The address is taken from the connection rather than
// the body, because the approval screen shows it and a client must not be able
// to write its own alibi.
func (h *lanHandler) serveRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if r.Body != nil {
		// A device that sends nothing still gets to ask; it is the name that is
		// optional, not the request.
		_ = json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body)
	}
	address := remoteHost(r)
	device, err := h.store.Request(body.Name, address)
	if err != nil {
		switch {
		case errors.Is(err, lanaccess.ErrTooManyPending):
			writeJSONMessage(w, http.StatusTooManyRequests, "too_many_requests",
				"Too many devices are already waiting. Ask the person at the drive to answer them first.")
		case errors.Is(err, lanaccess.ErrTooManyDevices):
			writeJSONMessage(w, http.StatusTooManyRequests, "too_many_devices",
				"This drive already knows as many devices as it will keep.")
		default:
			h.logger.Error("lan.request_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "request_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, lanRequestResponse{
		ID:      device.ID,
		State:   string(device.State),
		Message: "Ask the person at the drive to approve this device.",
	})
}

type lanClaimResponse struct {
	State string `json:"state"`
	// Token is the credential, returned exactly once, on the first poll after
	// approval. The device stores it and uses it as its password from then on.
	Token string `json:"token,omitempty"`
}

// serveClaim lets a waiting device collect its answer.
func (h *lanHandler) serveClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, lanRequestPrefix)
	if strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown request.")
		return
	}
	device, token, claimed, err := h.store.Claim(id)
	if err != nil {
		switch {
		case errors.Is(err, lanaccess.ErrDenied):
			writeJSON(w, http.StatusOK, lanClaimResponse{State: string(lanaccess.StateDenied)})
		case errors.Is(err, lanaccess.ErrNotFound):
			// A request that expired, or an id that never existed. The device is
			// told to start again rather than left polling forever.
			writeJSONMessage(w, http.StatusNotFound, "not_found",
				"This request is no longer waiting. Ask again from the drive's page.")
		default:
			h.logger.Error("lan.claim_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "claim_failed", err.Error())
		}
		return
	}
	response := lanClaimResponse{State: string(device.State)}
	if claimed {
		response.Token = token
	}
	writeJSON(w, http.StatusOK, response)
}

type lanDevicesResponse struct {
	Devices []lanaccess.Public `json:"devices"`
	Pending []lanaccess.Public `json:"pending"`
	// RequestPath and PollPath are what a device needs to ask and to collect its
	// answer, so the page that explains the flow cannot disagree with the server
	// about where they are.
	RequestPath string `json:"requestPath"`
	PollPath    string `json:"pollPath"`
}

// serveDevices is the owner's view: everything waiting, and everything decided.
func (h *lanHandler) serveDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := authorizeManager(w, r, h.auth,
		"Only an instance owner or administrator can manage local-network access."); !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
		return
	}
	writeJSON(w, http.StatusOK, h.devicesResponse())
}

// devicesResponse is the owner's view, shared by every endpoint that changes it
// so the page never has to guess what happened.
func (h *lanHandler) devicesResponse() lanDevicesResponse {
	return lanDevicesResponse{
		Devices:     h.store.Devices(),
		Pending:     h.store.Pending(),
		RequestPath: lanRequestPath,
		PollPath:    lanRequestPrefix,
	}
}

// serveDevice approves, refuses or forgets one device.
func (h *lanHandler) serveDevice(w http.ResponseWriter, r *http.Request) {
	approverID, ok := authorizeManager(w, r, h.auth,
		"Only an instance owner or administrator can manage local-network access.")
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, lanDevicePrefix)
	id, action, _ := strings.Cut(rest, "/")
	if strings.TrimSpace(id) == "" {
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown device.")
		return
	}
	switch {
	case r.Method == http.MethodPost && action == "approve":
		// The approving account is whose session the device will borrow, so it
		// is recorded here rather than looked up later.
		if _, err := h.store.Approve(id, approverID); err != nil {
			h.writeDeviceError(w, err)
			return
		}
		h.logger.Info("lan.approved", "device", id, "owner", approverID)
	case r.Method == http.MethodPost && action == "deny":
		if _, err := h.store.Deny(id); err != nil {
			h.writeDeviceError(w, err)
			return
		}
		h.logger.Info("lan.denied", "device", id)
	case r.Method == http.MethodDelete && action == "":
		if err := h.store.Forget(id); err != nil {
			h.writeDeviceError(w, err)
			return
		}
	case action != "":
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown local-network action.")
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"Use POST /approve, POST /deny or DELETE.")
		return
	}
	writeJSON(w, http.StatusOK, h.devicesResponse())
}

func (h *lanHandler) writeDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lanaccess.ErrNotFound):
		writeJSONMessage(w, http.StatusNotFound, "not_found", "That device is not in the registry.")
	case errors.Is(err, lanaccess.ErrNotPending):
		writeJSONMessage(w, http.StatusConflict, "already_answered",
			"That request has already been answered. Revoke the device or forget it to change the decision.")
	default:
		h.logger.Error("lan.device_failed", "error", err)
		writeJSONMessage(w, http.StatusInternalServerError, "device_failed", err.Error())
	}
}

// remoteHost is the address the request actually came from, without its port.
// It is taken from the connection rather than from anything the client sent:
// the approval screen shows this, and a device must not be able to describe
// itself as something else.
func remoteHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// lanPollHint is how long a waiting device should wait between polls. It is
// reported so the page and the server agree on a pace.
const lanPollHint = 2 * time.Second
