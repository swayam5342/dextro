package files

import (
	"net/http"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

// transferMessages turns a stored failure into something safe and useful to show.
var transferMessages = map[string]string{
	failureSourceMissing: "This file is gone from its account.",
	failureSource:        "Its account couldn't be reached. Try again once it's working.",
	failureTarget:        "The other account refused the file, or couldn't be reached.",
	failureCopy:          "The copy stopped partway. The file is still in its old account.",
	failureShared:        "This file has a Google link now, and a move would break it. Remove the link first.",
	failureVerify:        "The copy didn't match the original, so it was thrown away. The file is still in its old account.",
}

func toAPITransfer(t Transfer) apigen.Transfer {
	transfer := apigen.Transfer{
		Id:                 t.ID,
		FileId:             t.FileID,
		Name:               t.Name,
		Size:               t.Size,
		Copied:             t.Copied,
		SourceConnectionId: t.SourceConnectionID,
		TargetConnectionId: t.TargetConnectionID,
		Status:             apigen.Waiting,
	}
	if t.Failure != "" {
		message := transferMessages[t.Failure]
		transfer.Status = apigen.Failed
		transfer.Failure = &message
	}
	return transfer
}

func toAPITransferResult(r TransferResult) apigen.TransferResult {
	return apigen.TransferResult{Queued: r.Queued, Skipped: r.Skipped}
}

// ListTransfers implements apigen.ServerInterface.
func (h *Handler) ListTransfers(w http.ResponseWriter, r *http.Request) {
	queue, err := h.service.ListTransfers(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := apigen.TransferQueue{
		Transfers:    make([]apigen.Transfer, 0, len(queue.Transfers)),
		Waiting:      int(queue.Waiting),
		WaitingBytes: queue.WaitingBytes,
		Failed:       int(queue.Failed),
		Removing:     int(queue.Removing),
		Accounts:     make([]apigen.AccountFiles, 0, len(queue.Accounts)),
	}
	for _, transfer := range queue.Transfers {
		body.Transfers = append(body.Transfers, toAPITransfer(transfer))
	}
	for _, account := range queue.Accounts {
		body.Accounts = append(body.Accounts, apigen.AccountFiles{
			ConnectionId:  account.ConnectionID,
			Files:         int(account.Files),
			ExternalFiles: int(account.External),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// CreateTransfers implements apigen.ServerInterface.
func (h *Handler) CreateTransfers(w http.ResponseWriter, r *http.Request) {
	var body apigen.NewTransfers
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	result, err := h.service.TransferFiles(r.Context(), userID(r), body.FileIds, body.TargetConnectionId, httpx.ClientIP(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPITransferResult(result))
}

// RetryTransfers implements apigen.ServerInterface.
func (h *Handler) RetryTransfers(w http.ResponseWriter, r *http.Request) {
	h.noContent(w, r, h.service.RetryTransfers(r.Context(), userID(r)))
}

// DeleteTransfer implements apigen.ServerInterface.
func (h *Handler) DeleteTransfer(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.CancelTransfer(r.Context(), userID(r), id))
}

// EmptyConnection implements apigen.ServerInterface.
func (h *Handler) EmptyConnection(w http.ResponseWriter, r *http.Request, id types.UUID) {
	result, err := h.service.EmptyConnection(r.Context(), userID(r), id, httpx.ClientIP(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPITransferResult(result))
}

// StopEmptyingConnection implements apigen.ServerInterface.
func (h *Handler) StopEmptyingConnection(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.StopEmptying(r.Context(), userID(r), id))
}
