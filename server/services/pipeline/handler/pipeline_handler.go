package handler

import (
	"log/slog"
	"net/http"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
)

type statusHandler struct {
	usecase pipeline.PipelineUsecase
	logger  *slog.Logger
}

// NewStatusHandlerImpl builds the pipeline status handler.
func NewStatusHandlerImpl(usecase pipeline.PipelineUsecase, logger *slog.Logger) pipeline.StatusHandler {
	return &statusHandler{usecase: usecase, logger: logger}
}

// Status answers GET /api/v1/status.
func (h *statusHandler) Status(w http.ResponseWriter, r *http.Request) {
	status, err := h.usecase.Status(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not assemble the pipeline status", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the pipeline status could not be assembled")
		return
	}
	helper.WriteAPIJSON(w, h.logger, http.StatusOK, pipeline.ToStatusResponse(status))
}
