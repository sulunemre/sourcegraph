package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/inconshreveable/log15"

	"github.com/sourcegraph/log"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/backend"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/globals"
	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/conf"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/errcode"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/jsonc"
	"github.com/sourcegraph/sourcegraph/internal/txemail"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

func serveReposGetByName(db database.DB) func(http.ResponseWriter, *http.Request) error {
	logger := log.Scoped("serveReposGetByName", "")
	return func(w http.ResponseWriter, r *http.Request) error {
		repoName := api.RepoName(mux.Vars(r)["RepoName"])
		repo, err := backend.NewRepos(logger, db).GetByName(r.Context(), repoName)
		if err != nil {
			return err
		}
		data, err := json.Marshal(repo)
		if err != nil {
			return err
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return nil
	}
}

func servePhabricatorRepoCreate(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var repo api.PhabricatorRepoCreateRequest
		err := json.NewDecoder(r.Body).Decode(&repo)
		if err != nil {
			return err
		}
		phabRepo, err := db.Phabricator().CreateOrUpdate(r.Context(), repo.Callsign, repo.RepoName, repo.URL)
		if err != nil {
			return err
		}
		data, err := json.Marshal(phabRepo)
		if err != nil {
			return err
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return nil
	}
}

// serveExternalServiceConfigs serves a JSON response that is an array of all
// external service configs that match the requested kind.
func serveExternalServiceConfigs(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.ExternalServiceConfigsRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return err
		}

		options := database.ExternalServicesListOptions{
			Kinds:   []string{req.Kind},
			AfterID: int64(req.AfterID),
		}
		if req.Limit > 0 {
			options.LimitOffset = &database.LimitOffset{
				Limit: req.Limit,
			}
		}

		services, err := db.ExternalServices().List(r.Context(), options)
		if err != nil {
			return err
		}

		// Instead of returning an intermediate response type, we directly return
		// the array of configs (which are themselves JSON objects).
		// This makes it possible for the caller to directly unmarshal the response into
		// a slice of connection configurations for this external service kind.
		configs := make([]map[string]any, 0, len(services))
		for _, service := range services {
			var config map[string]any
			// Raw configs may have comments in them so we have to use a json parser
			// that supports comments in json.
			if err := jsonc.Unmarshal(service.Config, &config); err != nil {
				log15.Error(
					"ignoring external service config that has invalid json",
					"id", service.ID,
					"displayName", service.DisplayName,
					"config", service.Config,
					"err", err,
				)
				continue
			}
			configs = append(configs, config)
		}
		return json.NewEncoder(w).Encode(configs)
	}
}

// serveExternalServicesList serves a JSON response that is an array of all external services
// of the given kind
func serveExternalServicesList(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.ExternalServicesListRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return err
		}

		if len(req.Kinds) == 0 {
			req.Kinds = append(req.Kinds, req.Kind)
		}

		options := database.ExternalServicesListOptions{
			Kinds:   []string{req.Kind},
			AfterID: int64(req.AfterID),
		}
		if req.Limit > 0 {
			options.LimitOffset = &database.LimitOffset{
				Limit: req.Limit,
			}
		}

		services, err := db.ExternalServices().List(r.Context(), options)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(services)
	}
}

func serveConfiguration(w http.ResponseWriter, _ *http.Request) error {
	raw := conf.Raw()
	err := json.NewEncoder(w).Encode(raw)
	if err != nil {
		return errors.Wrap(err, "Encode")
	}
	return nil
}

func serveSettingsGetForSubject(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var subject api.SettingsSubject
		if err := json.NewDecoder(r.Body).Decode(&subject); err != nil {
			return errors.Wrap(err, "Decode")
		}
		settings, err := db.Settings().GetLatest(r.Context(), subject)
		if err != nil {
			return errors.Wrap(err, "Settings.GetLatest")
		}
		if err := json.NewEncoder(w).Encode(settings); err != nil {
			return errors.Wrap(err, "Encode")
		}
		return nil
	}
}

func serveOrgsListUsers(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var orgID int32
		err := json.NewDecoder(r.Body).Decode(&orgID)
		if err != nil {
			return errors.Wrap(err, "Decode")
		}
		orgMembers, err := db.OrgMembers().GetByOrgID(r.Context(), orgID)
		if err != nil {
			return errors.Wrap(err, "OrgMembers.GetByOrgID")
		}
		users := make([]int32, 0, len(orgMembers))
		for _, member := range orgMembers {
			users = append(users, member.UserID)
		}
		if err := json.NewEncoder(w).Encode(users); err != nil {
			return errors.Wrap(err, "Encode")
		}
		return nil
	}
}

func serveOrgsGetByName(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var orgName string
		err := json.NewDecoder(r.Body).Decode(&orgName)
		if err != nil {
			return errors.Wrap(err, "Decode")
		}
		org, err := db.Orgs().GetByName(r.Context(), orgName)
		if err != nil {
			return errors.Wrap(err, "Orgs.GetByName")
		}
		if err := json.NewEncoder(w).Encode(org.ID); err != nil {
			return errors.Wrap(err, "Encode")
		}
		return nil
	}
}

func serveUserEmailsGetEmail(db database.DB) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var userID int32
		err := json.NewDecoder(r.Body).Decode(&userID)
		if err != nil {
			return errors.Wrap(err, "Decode")
		}
		email, _, err := db.UserEmails().GetPrimaryEmail(r.Context(), userID)
		if err != nil {
			return errors.Wrap(err, "UserEmails.GetEmail")
		}
		if err := json.NewEncoder(w).Encode(email); err != nil {
			return errors.Wrap(err, "Encode")
		}
		return nil
	}
}

func serveExternalURL(w http.ResponseWriter, r *http.Request) error {
	if err := json.NewEncoder(w).Encode(globals.ExternalURL().String()); err != nil {
		return errors.Wrap(err, "Encode")
	}
	return nil
}

func serveSendEmail(w http.ResponseWriter, r *http.Request) error {
	var msg txemail.Message
	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		return err
	}
	return txemail.Send(r.Context(), msg)
}

func serveGitResolveRevision(db database.DB) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		// used by zoekt-sourcegraph-mirror
		vars := mux.Vars(r)
		name := api.RepoName(vars["RepoName"])
		spec := vars["Spec"]

		// Do not to trigger a repo-updater lookup since this is a batch job.
		commitID, err := gitserver.NewClient(db).ResolveRevision(r.Context(), name, spec, gitserver.ResolveRevisionOptions{})
		if err != nil {
			return err
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(commitID))
		return nil
	}
}

// gitServiceHandler are handlers which redirect git clone requests to the
// gitserver for the repo.
type gitServiceHandler struct {
	Gitserver interface {
		AddrForRepo(context.Context, api.RepoName) (string, error)
	}
}

func (s *gitServiceHandler) serveInfoRefs() func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		return s.redirectToGitServer(w, r, "/info/refs")
	}
}

func (s *gitServiceHandler) serveGitUploadPack() func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		return s.redirectToGitServer(w, r, "/git-upload-pack")
	}
}

func (s *gitServiceHandler) redirectToGitServer(w http.ResponseWriter, r *http.Request, gitPath string) error {
	repo := mux.Vars(r)["RepoName"]

	addrForRepo, err := s.Gitserver.AddrForRepo(r.Context(), api.RepoName(repo))
	if err != nil {
		return err
	}
	u := &url.URL{
		Scheme:   "http",
		Host:     addrForRepo,
		Path:     path.Join("/git", repo, gitPath),
		RawQuery: r.URL.RawQuery,
	}

	http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
	return nil
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	_, _ = w.Write([]byte("pong"))
}

func newServiceRegisterHandler(db database.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, err := ipFromRequest(r)
		if err != nil {
			http.Error(w, "could not extract IP address", http.StatusBadRequest)
			return
		}

		vars := mux.Vars(r)

		args := database.ServiceArgs{}
		err = json.NewDecoder(r.Body).Decode(&args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check required args.
		if args.Port == 0 {
			http.Error(w, "missing port", http.StatusBadRequest)
			return
		}
		args.IP = ip

		id, err := db.Services().Register(r.Context(), vars["name"], args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write([]byte(id))
	}
}

func newServiceRenewHandler(db database.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		err := db.Services().Renew(r.Context(), vars["name"], vars["instanceID"])
		if err != nil {
			if errcode.IsNotFound(err) {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}
}

func newServiceDeregisterHandler(db database.DB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		err := db.Services().Deregister(r.Context(), vars["name"], vars["instanceID"])
		if err != nil {
			if err != nil {
				if errcode.IsNotFound(err) {
					http.Error(w, err.Error(), http.StatusNotFound)
				} else {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				return
			}
		}
	}
}

func ipFromRequest(r *http.Request) (netip.Addr, error) {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return netip.ParseAddr(strings.Split(v, ",")[0])
	}
	return netip.ParseAddr(strings.Split(r.RemoteAddr, ":")[0])
}
