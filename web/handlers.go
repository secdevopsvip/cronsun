package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/coreos/etcd/clientv3"
	"github.com/gorilla/mux"

	"sunteng/commons/log"
	"sunteng/cronsun/conf"
	"sunteng/cronsun/models"
)

func InitRouters() (s *http.Server, err error) {
	r := mux.NewRouter()
	subrouter := r.PathPrefix("/v1").Subrouter()

	h := BaseHandler{Handle: getJobGroups}
	subrouter.Handle("/job/groups", h).Methods("GET")

	h = BaseHandler{Handle: getJobsByGroupName}
	subrouter.Handle("/job/group/{name}", h).Methods("GET")

	h = BaseHandler{Handle: updateJob}
	subrouter.Handle("/job", h).Methods("PUT")

	h = BaseHandler{Handle: getNodeGroups}
	subrouter.Handle("/node/groups", h).Methods("GET")

	h = BaseHandler{Handle: getNodeGroupByName}
	subrouter.Handle("/node/group/{name}", h).Methods("GET")

	h = BaseHandler{Handle: nodeJoinGroup}
	subrouter.Handle("/node/group", h).Methods("PUT")

	h = BaseHandler{Handle: nodeLeaveGroup}
	subrouter.Handle("/node/group", h).Methods("DELETE")

	s = &http.Server{
		Handler: r,
	}
	return s, nil
}

var cmdKeyDeepLen = len(strings.Split(conf.Config.Cmd, "/"))

func getJobGroups(w http.ResponseWriter, r *http.Request) {
	resp, err := models.DefalutClient.Get(conf.Config.Cmd, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var groupMap = make(map[string]bool, 8)
	for i := range resp.Kvs {
		ss := strings.Split(string(resp.Kvs[i].Key), "/")
		groupMap[ss[cmdKeyDeepLen]] = true
	}

	var groupList = make([]string, 0, len(groupMap))
	for k := range groupMap {
		groupList = append(groupList, k)
	}

	sort.Strings(groupList)
	outJSON(w, groupList)
}

func getJobsByGroupName(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	resp, err := models.DefalutClient.Get(path.Join(conf.Config.Cmd, vars["name"]), clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var jobList = make([]*models.Job, 0, resp.Count)
	for i := range resp.Kvs {
		job := &models.Job{}
		err = json.Unmarshal(resp.Kvs[i].Value, &job)
		if err != nil {
			outJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jobList = append(jobList)
	}

	outJSON(w, jobList)
}

func updateJob(w http.ResponseWriter, r *http.Request) {
	job := &models.Job{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&job)
	if err != nil {
		outJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	r.Body.Close()

	var creation bool
	if len(job.ID) == 0 {
		creation = true
		job.ID = models.NextID()
	}

	jobb, err := json.Marshal(job)
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, err = models.DefalutClient.Put(path.Join(conf.Config.Cmd, job.Group, job.ID), string(jobb))
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	statusCode := http.StatusOK
	if creation {
		statusCode = http.StatusCreated
	}
	outJSONWithCode(w, statusCode, nil)
}

var ngKeyDeepLen = len(conf.Config.Group)

func getNodeGroups(w http.ResponseWriter, r *http.Request) {
	resp, err := models.DefalutClient.Get(conf.Config.Group, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var groupMap = make(map[string]bool, 8)
	for i := range resp.Kvs {
		ss := strings.Split(string(resp.Kvs[i].Key), "/")
		groupMap[ss[ngKeyDeepLen]] = true
	}

	var groupList = make([]string, 0, len(groupMap))
	for k := range groupMap {
		groupList = append(groupList, k)
	}

	sort.Strings(groupList)
	outJSON(w, groupList)
}

func getNodeGroupByName(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	resp, err := models.DefalutClient.Get(path.Join(conf.Config.Group, vars["name"]), clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var nodeList = make([]*models.Node, 0, resp.Count)
	for i := range resp.Kvs {
		node := &models.Node{}
		err = json.Unmarshal(resp.Kvs[i].Value, &node)
		if err != nil {
			outJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nodeList = append(nodeList)
	}

	outJSON(w, nodeList)
}

func nodeJoinGroup(w http.ResponseWriter, r *http.Request) {
	ng := []struct {
		Nodes []string
		Group string
	}{}

	de := json.NewDecoder(r.Body)
	err := de.Decode(&ng)
	if err != nil {
		outJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	gresp, err := models.DefalutClient.Get(conf.Config.Proc, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		log.Errorf("get nodes list failed: %s", err.Error())
		outJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var nodes map[string]bool
	for i := range gresp.Kvs {
		ip := strings.TrimLeft(string(gresp.Kvs[i].Key), conf.Config.Proc)
		nodes[ip] = true
	}

	var errMsg string
	var status int
NGLOOP:
	for i := range ng {
		g := strings.TrimSpace(ng[i].Group)
		if len(g) == 0 {
			errMsg = "group name is emtpy."
			status = http.StatusBadRequest
			break
		}

		for _, n := range ng[i].Nodes {
			n = strings.TrimSpace(n)
			if len(n) == 0 {
				errMsg = fmt.Sprintf("[%s] node ip is emtpy.", g)
				status = http.StatusBadRequest
				break NGLOOP
			}

			if _, ok := nodes[n]; !ok {
				errMsg = fmt.Sprintf("node[%s] not found.", n)
				status = http.StatusBadRequest
				break NGLOOP
			}

			_, err = models.DefalutClient.Put(path.Join(conf.Config.Group, g, n), "")
			if err != nil {
				errMsg = "join failed: " + err.Error()
				status = http.StatusInternalServerError
				break NGLOOP
			}
		}
	}

	if len(errMsg) > 0 {
		outJSONError(w, status, errMsg)
		return
	}

	outJSON(w, nil)
}

func nodeLeaveGroup(w http.ResponseWriter, r *http.Request) {}
