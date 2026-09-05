package server

import (
	"encoding/json"
	"net/http"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/sandbox"
)

// handleGetWorkspaceSandbox reports whether the workspace's process is
// currently confined by a sandbox.
//
//	@Summary		Get sandbox status
//	@Tags			sandbox
//	@Produce		json
//	@Param			id	path		string	true	"Workspace ID"
//	@Success		200	{object}	proto.SandboxStatusResponse
//	@Failure		404	{object}	proto.Error
//	@Failure		500	{object}	proto.Error
//	@Router			/workspaces/{id}/sandbox [get]
func (c *controllerV1) handleGetWorkspaceSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inSandbox, err := c.backend.IsInSandbox(id)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, proto.SandboxStatusResponse{InSandbox: inSandbox})
}

// handlePostWorkspaceSandboxEnter restricts the workspace's process to
// the given filesystem paths and, optionally, network access.
//
//	@Summary		Enter sandbox
//	@Tags			sandbox
//	@Accept			json
//	@Param			id		path	string					true	"Workspace ID"
//	@Param			request	body	proto.EnterSandboxRequest	true	"Sandbox restrictions"
//	@Success		200
//	@Failure		400	{object}	proto.Error
//	@Failure		404	{object}	proto.Error
//	@Failure		500	{object}	proto.Error
//	@Router			/workspaces/{id}/sandbox/enter [post]
func (c *controllerV1) handlePostWorkspaceSandboxEnter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req proto.EnterSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.server.logError(r, "Failed to decode request", "error", err)
		jsonError(w, http.StatusBadRequest, "failed to decode request")
		return
	}

	cfg := sandbox.Config{
		ReadWrite:    req.ReadWrite,
		ReadOnly:     req.ReadOnly,
		AllowNetwork: req.AllowNetwork,
	}
	if err := c.backend.EnterSandbox(r.Context(), id, cfg); err != nil {
		c.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
