package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/emicklei/go-restful"
	git "github.com/gogits/git-module"
	"github.com/gorilla/mux"
	"github.com/kuberlab/pacak/pkg/pacakimpl"
)

type pacakAPI struct {
	git pacakimpl.GitInterface
}

func StartAPI(git pacakimpl.GitInterface) {
	r := mux.NewRouter()
	r.NotFoundHandler = NotFoundHandler()
	container := restful.NewContainer()
	container.EnableContentEncoding(false)
	ws := new(restful.WebService)
	ws.Path("/api/v1")
	ws.ApiVersion("v1")
	ws.Produces(restful.MIME_JSON)

	api := pacakAPI{
		git: git,
	}
	ws.Route(ws.POST("/git/init/{repo}").To(api.Init))
	ws.Route(ws.POST("/git/commit/{repo}/{file}").To(api.Commit))
	ws.Route(ws.GET("/git/commits/{repo}").To(api.Commits))
	container.Add(ws)
	r.PathPrefix("/api/v1/").Handler(container)
	logrus.Infoln("Listen in *:8082")
	if err := http.ListenAndServe(":8082", WrapLogger(r)); err != nil {
		logrus.Errorln(err)
		os.Exit(1)
	}
}

func (api pacakAPI) Init(req *restful.Request, resp *restful.Response) {
	repo := "test/" + req.PathParameter("repo")

	logrus.Infoln("Init: %v", repo)
	if err := api.git.InitRepository(Signature(req), repo, nil); err != nil {
		resp.WriteError(http.StatusInternalServerError, err)
	} else {
		resp.WriteHeader(http.StatusNoContent)
	}
}

func (api pacakAPI) Commits(req *restful.Request, resp *restful.Response) {
	repo := "test/" + req.PathParameter("repo")
	gitRepo, err := api.git.GetRepository(repo)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError, err)
		return
	}
	commits, err := gitRepo.Commits("", func(_ string) bool { return true })
	if err != nil {
		resp.WriteError(http.StatusInternalServerError, err)
		return
	}
	resp.WriteEntity(commits)
}
func (api pacakAPI) Commit(req *restful.Request, resp *restful.Response) {
	repo := "test/" + req.PathParameter("repo")
	file := req.PathParameter("file")
	data, err := ioutil.ReadAll(req.Request.Body)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError, err)
		return
	}
	gitRepo, err := api.git.GetRepository(repo)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError, err)
		return
	}
	for i := 0 ; i < 5 ; i++ {
		go func() {
			commit, err := gitRepo.Save(Signature(req), "new branch1", "master", "master", []pacakimpl.GitFile{
				{
					Path: file,
					Data: data,
				},
			})
			if err != nil {
				panic(err)

			} else {
				fmt.Println("Commit: ",commit)
			}
		}()
		go func() {
			_, err := gitRepo.Commits("", func(_ string) bool {
				return true
			})
			if err != nil {
				panic(err)
			}
		}()
	}
	resp.WriteEntity("ok")

}

func NotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)

		resp := APIError{
			Status:  http.StatusNotFound,
			Message: fmt.Sprintf("URI '%v' not found", r.RequestURI),
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		w.Write(data)
		w.Write([]byte("\n"))
	})
}

func Signature(req *restful.Request) git.Signature {
	email := req.HeaderParameter("GIT_EMAIL")
	if email == "" {
		email = "pacak@kuberlab.com"
	}
	name := req.HeaderParameter("GIT_NAME")
	if name == "" {
		name = "pacak"
	}
	return git.Signature{
		Email: email,
		Name:  name,
		When:  time.Now(),
	}
}

type LogRecordHandler struct {
	http.ResponseWriter
	status int
}

func (r *LogRecordHandler) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *LogRecordHandler) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (r *LogRecordHandler) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("not a Hijacker")
}

func WrapLogger(f http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record := &LogRecordHandler{
			ResponseWriter: w,
			status:         200,
		}
		t := time.Now()
		f.ServeHTTP(record, r)
		logrus.Infof("%v %v => %v, %v", r.Method, r.RequestURI, record.status, time.Since(t))
	})
}
