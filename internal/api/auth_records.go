package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func authRecordIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	collection, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	record := uuid.Nil
	if value := r.PathValue("record"); value != "" {
		record, err = uuid.Parse(value)
	}
	return collection, record, err
}

func (e authCollectionEndpoints) adminRecordList(w http.ResponseWriter, r *http.Request) {
	collection, _, err := authRecordIDs(r)
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	items, err := e.dependencies.AuthCollections.ListAuthRecords(r.Context(), collection, limit)
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []userauth.AuthRecord `json:"items"`
	}{items})
}
func (e authCollectionEndpoints) adminRecordGet(w http.ResponseWriter, r *http.Request) {
	collection, record, err := authRecordIDs(r)
	if err != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	value, err := e.dependencies.AuthCollections.GetAuthRecord(r.Context(), collection, record)
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (e authCollectionEndpoints) adminRecordCreate(w http.ResponseWriter, r *http.Request) {
	collection, _, err := authRecordIDs(r)
	var in struct {
		Email           string         `json:"email"`
		EmailVisibility bool           `json:"emailVisibility"`
		Password        string         `json:"password"`
		Verified        bool           `json:"verified"`
		Active          *bool          `json:"active"`
		Fields          map[string]any `json:"fields"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	value, err := e.dependencies.AuthCollections.CreateAuthRecord(r.Context(), collection, userauth.CreateAuthRecordInput{Email: in.Email, EmailVisibility: in.EmailVisibility, Password: in.Password, Verified: in.Verified, Active: active, Fields: in.Fields})
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}
func (e authCollectionEndpoints) adminRecordUpdate(w http.ResponseWriter, r *http.Request) {
	collection, record, err := authRecordIDs(r)
	var in struct {
		Email           *string        `json:"email"`
		EmailVisibility *bool          `json:"emailVisibility"`
		Password        *string        `json:"password"`
		Verified        *bool          `json:"verified"`
		Active          *bool          `json:"active"`
		Fields          map[string]any `json:"fields"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	value, err := e.dependencies.AuthCollections.UpdateAuthRecord(r.Context(), collection, record, userauth.UpdateAuthRecordInput{Email: in.Email, EmailVisibility: in.EmailVisibility, Password: in.Password, Verified: in.Verified, Active: in.Active, Fields: in.Fields})
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (e authCollectionEndpoints) adminRecordDelete(w http.ResponseWriter, r *http.Request) {
	collection, record, err := authRecordIDs(r)
	if err != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	if err := e.dependencies.AuthCollections.DeleteAuthRecord(r.Context(), collection, record); err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e authCollectionEndpoints) auxiliaryList(w http.ResponseWriter, r *http.Request) {
	current, err := e.current(r)
	collection, resolveErr := e.resolve(r)
	if err != nil || resolveErr != nil || current.principal.User.AuthCollectionID != collection.ID {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	items, err := e.dependencies.AuthCollections.ListAuthAuxiliary(r.Context(), collection.ID, current.principal.User.ID, r.PathValue("kind"))
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []userauth.AuthAuxiliaryRecord `json:"items"`
	}{items})
}

func (e authCollectionEndpoints) auxiliaryDelete(w http.ResponseWriter, r *http.Request) {
	current, err := e.current(r)
	collection, resolveErr := e.resolve(r)
	id, idErr := uuid.Parse(r.PathValue("id"))
	if err != nil || resolveErr != nil || idErr != nil || current.principal.User.AuthCollectionID != collection.ID {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	if err := e.dependencies.AuthCollections.DeleteAuthAuxiliary(r.Context(), collection.ID, current.principal.User.ID, r.PathValue("kind"), id); err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e authCollectionEndpoints) auxiliaryGet(w http.ResponseWriter, r *http.Request) {
	current, err := e.current(r)
	collection, resolveErr := e.resolve(r)
	id, idErr := uuid.Parse(r.PathValue("id"))
	if err != nil || resolveErr != nil || idErr != nil || current.principal.User.AuthCollectionID != collection.ID {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	items, err := e.dependencies.AuthCollections.ListAuthAuxiliary(r.Context(), collection.ID, current.principal.User.ID, r.PathValue("kind"))
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	for _, item := range items {
		if item.ID == id {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	writeAuthCollectionError(w, r, userauth.ErrNotFound)
}

func (e authCollectionEndpoints) authorizedRecordContext(r *http.Request) (userauth.AuthCollection, userauth.Principal, uuid.UUID, error) {
	current, err := e.current(r)
	collection, err := e.resolve(r)
	if err != nil {
		return userauth.AuthCollection{}, userauth.Principal{}, uuid.Nil, userauth.ErrInvalidCredentials
	}
	principal := userauth.Principal{}
	if current.principal.User.ID != uuid.Nil {
		if current.principal.User.AuthCollectionID != collection.ID {
			return userauth.AuthCollection{}, userauth.Principal{}, uuid.Nil, userauth.ErrInvalidCredentials
		}
		principal = current.principal
	} else if subject, ok := r.Context().Value(publicRecordPrincipalKey{}).(identity.Subject); !ok || subject.Kind != identity.Anonymous {
		return userauth.AuthCollection{}, userauth.Principal{}, uuid.Nil, userauth.ErrInvalidCredentials
	}
	target := uuid.Nil
	if raw := r.PathValue("record"); raw != "" {
		target, err = uuid.Parse(raw)
	}
	return collection, principal, target, err
}
func (e authCollectionEndpoints) authorizedRecordList(w http.ResponseWriter, r *http.Request) {
	collection, principal, _, err := e.authorizedRecordContext(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	items, err := e.dependencies.AuthCollections.ListAuthorizedAuthRecords(r.Context(), collection.ID, principal.User.ID, 50)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []userauth.AuthRecord `json:"items"`
	}{items})
}
func (e authCollectionEndpoints) authorizedRecordGet(w http.ResponseWriter, r *http.Request) {
	collection, principal, target, err := e.authorizedRecordContext(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	value, err := e.dependencies.AuthCollections.GetAuthorizedAuthRecord(r.Context(), collection.ID, principal.User.ID, target)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (e authCollectionEndpoints) authorizedRecordUpdate(w http.ResponseWriter, r *http.Request) {
	collection, principal, target, err := e.authorizedRecordContext(r)
	var in struct {
		Email           *string        `json:"email"`
		EmailVisibility *bool          `json:"emailVisibility"`
		Password        *string        `json:"password"`
		Verified        *bool          `json:"verified"`
		Active          *bool          `json:"active"`
		Fields          map[string]any `json:"fields"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	value, err := e.dependencies.AuthCollections.UpdateAuthorizedAuthRecord(r.Context(), collection.ID, principal.User.ID, target, userauth.UpdateAuthRecordInput{Email: in.Email, EmailVisibility: in.EmailVisibility, Password: in.Password, Verified: in.Verified, Active: in.Active, Fields: in.Fields})
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (e authCollectionEndpoints) authorizedRecordDelete(w http.ResponseWriter, r *http.Request) {
	collection, principal, target, err := e.authorizedRecordContext(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	if err := e.dependencies.AuthCollections.DeleteAuthorizedAuthRecord(r.Context(), collection.ID, principal.User.ID, target); err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
