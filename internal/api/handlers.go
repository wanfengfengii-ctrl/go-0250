package api

import (
	"encoding/json"
	"net/http"

	"github.com/windowproof/fenestration/internal/service"
)

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var in service.CreateTaskRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	body, err := s.svc.CreateTask(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	var in service.LockRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.Lock(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleInstallation(w http.ResponseWriter, r *http.Request) {
	var in service.InstallationRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.ConfirmInstallation(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleReplaceChamber(w http.ResponseWriter, r *http.Request) {
	var in service.ReplaceChamberRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.ReplaceChamber(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handlePressureStep(w http.ResponseWriter, r *http.Request) {
	var in service.PressureStepRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.SubmitPressureStep(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleSprayCheckpoint(w http.ResponseWriter, r *http.Request) {
	var in service.SprayCheckpointRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.SubmitSprayCheckpoint(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleRetryAttempt(w http.ResponseWriter, r *http.Request) {
	var in service.RetryAttemptRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	in.AttemptID = r.PathValue("attemptID")
	body, err := s.svc.RetryAttempt(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleDefect(w http.ResponseWriter, r *http.Request) {
	var in service.DefectRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.AddDefect(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleRepair(w http.ResponseWriter, r *http.Request) {
	var in service.RepairRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.StartRepair(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleCloseDefect(w http.ResponseWriter, r *http.Request) {
	var in service.CloseDefectRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	in.EvidenceID = r.PathValue("evidenceID")
	body, err := s.svc.CloseDefect(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var in service.ReviewRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.SubmitReview(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var in service.TerminalRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.Release(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleQuarantine(w http.ResponseWriter, r *http.Request) {
	var in service.TerminalRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.Quarantine(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var in service.TerminalRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("id")
	body, err := s.svc.Cancel(r.Context(), in)
	writeResult(w, body, err)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}
