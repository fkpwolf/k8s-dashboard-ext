package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"text/template"

	"github.com/google/uuid"
	goji "goji.io"
	"goji.io/pat"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type NodeResourceUsage struct { // 主机 CPU 或者内存
	Name         string  `bson:"name"`
	Compare      string  `bson:"compare"`
	Threshold    float32 `bson:"threshold"`
	Severity     string  `bson:"severity"`
	Description  string  `bson:"description"`
	Node         string  `bson:"node"`
	ID           string  `bsone:"id"`
	Period       string  `bsone:"period"`
	Target       string  `bsone:"target"`
	Function     string  `bsone:"function"`
	Enabled      bool    `bsone:"enabled"`
	ResourceType string  `bsone:"resourcetype"`
}

type Resp struct {
	Data []NodeResourceUsage
}

var isDropMe = true

const (
	Username   = "YOUR_USERNAME"
	Password   = "YOUR_PASS"
	Database   = "K8S_DASHBOARD_DB"
	Collection = "RULE_COLLECTION"
)

func main() {
	DBHost := []string{
		"192.168.1.140:30275",
		// "dashboard-db-mongodb:27017",
		// replica set addrs...
	}

	session, err := mgo.DialWithInfo(&mgo.DialInfo{
		Addrs: DBHost,
		// Username: Username,
		// Password: Password,
		// Database: Database,
		// DialServer: func(addr *mgo.ServerAddr) (net.Conn, error) {
		// 	return tls.Dial("tcp", addr.String(), &tls.Config{})
		// },
	})
	if err != nil {
		panic(err)
	}
	defer session.Close()

	mux := goji.NewMux()
	mux.HandleFunc(pat.Get("/hello"), func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "dashboard ext server!")
	})
	mux.HandleFunc(pat.Post("/api/v2/alerts/rule"), addRule(session))
	mux.HandleFunc(pat.Get("/api/v2/alerts/rule"), allRules(session))
	mux.HandleFunc(pat.Delete("/api/v2/alerts/rule/:id"), deleteRule(session))
	mux.HandleFunc(pat.Post("/api/v2/alerts/rule/:id/toggle"), toggleRule(session))
	http.ListenAndServe(":8080", mux)
}

func addRule(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		var rule NodeResourceUsage
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&rule)
		if err != nil {
			errorWithJSON(w, "Incorrect body", http.StatusBadRequest)
			return
		}
		uuid, _ := uuid.NewRandom()
		rule.ID = uuid.String()
		rule.Enabled = true

		c := session.DB(Database).C(Collection)

		err = c.Insert(rule)
		if err != nil {
			if mgo.IsDup(err) {
				errorWithJSON(w, "Book with this ISBN already exists", http.StatusBadRequest)
				return
			}

			errorWithJSON(w, "Database error", http.StatusInternalServerError)
			log.Println("Failed insert book: ", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", r.URL.Path+"/"+rule.Node)
		w.WriteHeader(http.StatusCreated)

		sync(session)
	}
}

func deleteRule(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		id := pat.Param(r, "id")

		c := session.DB(Database).C(Collection)

		err := c.Remove(bson.M{"id": id})
		if err != nil {
			switch err {
			default:
				errorWithJSON(w, "Database error", http.StatusInternalServerError)
				log.Println("Failed delete book: ", err)
				return
			case mgo.ErrNotFound:
				errorWithJSON(w, "Book not found", http.StatusNotFound)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)

		sync(s)
	}
}

func toggleRule(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		id := pat.Param(r, "id")

		c := session.DB(Database).C(Collection)

		var rule NodeResourceUsage
		c.Find(bson.M{"id": id}).One(&rule)
		rule.Enabled = !rule.Enabled
		err := c.Update(bson.M{"id": rule.ID}, &rule)
		if err != nil {
			switch err {
			default:
				errorWithJSON(w, "Database error", http.StatusInternalServerError)
				log.Println("Failed update book: ", err)
				return
			case mgo.ErrNotFound:
				errorWithJSON(w, "Rule not found", http.StatusNotFound)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)

		sync(s)
	}
}

func allRules(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		c := session.DB(Database).C(Collection)

		// Find All
		rules := []NodeResourceUsage{}
		err := c.Find(nil).Sort("-start").All(&rules)
		if err != nil {
			errorWithJSON(w, "Database error", http.StatusInternalServerError)
			log.Println("Failed get all books: ", err)
			return
		}

		respBody, err := json.MarshalIndent(Resp{Data: rules}, "", "  ")
		if err != nil {
			log.Fatal(err)
		}

		responseWithJSON(w, respBody, http.StatusOK)
	}

}

const cpuTemplate = `ALERT NodeCPUUsage
	IF (100 - (avg by (instance) (irate(node_cpu{mode="idle"}[5m])) * 100)) [[.Compare]] [[.Threshold]]  
	FOR [[.Period]]
	LABELS { severity="[[.Severity]]"}
	ANNOTATIONS {SUMMARY = "{{$labels.instance}}: 检测到高 CPU 使用率",
		DESCRIPTION = "{{$labels.instance}}: CPU 使用率 is above [[.Threshold]]% (current value is: {{ $value }})"}`
const memTemplate = `ALERT NodeMemoryUsage
	IF (((node_memory_MemTotal - node_memory_MemFree - node_memory_Cached) / (node_memory_MemTotal) * 100)) [[.Compare]] [[.Threshold]]
	FOR [[.Period]]
	LABELS {severity="[[.Severity]]"}
	ANNOTATIONS {DESCRIPTION="{{$labels.instance}}: 内存用量 is above [[.Threshold]]% (当前值: {{ $value }})", 
					SUMMARY="[[.Description]]"}`
const containerCPUTemplate = `ALERT ContainerCPUUsage
	IF sum(rate(container_cpu_usage_seconds_total{name=~".+"}[5m])) BY (name) * 100 [[.Compare]] [[.Threshold]]  
	FOR [[.Period]]
	LABELS { severity="[[.Severity]]"}
	ANNOTATIONS {DESCRIPTION="{{$labels.name}}: 容器 CPU 使用率 is above 1% (current value is: {{ $value }})", 
					SUMMARY="[[.Description]]"}`
const containerMemTemplate = `ALERT ContainerCPUUsage
	IF sum(rate(container_cpu_usage_seconds_total{name=~".+"}[5m])) BY (name) * 100 [[.Compare]] [[.Threshold]]  
	FOR [[.Period]]
	LABELS { severity="[[.Severity]]"}
	ANNOTATIONS {DESCRIPTION="{{$labels.name}}: 容器 CPU 使用率 is above 1% (current value is: {{ $value }})", 
					SUMMARY="[[.Description]]"}`

const configMapName = "prometheus-rules"

func sync(s *mgo.Session) {
	fmt.Println("----do sync----")
	session := s.Copy()
	defer session.Close()

	c := session.DB(Database).C(Collection)

	// Find All
	var rules []NodeResourceUsage
	err := c.Find(bson.M{"enabled": true}).Sort("-start").All(&rules)
	if err != nil {
		log.Println("Failed get all rules: ", err)
		return
	}

	memTmpl, _ := template.New("mem-template").Delims("[[", "]]").Parse(memTemplate)
	cpuTmpl, _ := template.New("cpu-template").Delims("[[", "]]").Parse(cpuTemplate)
	containerMemTmpl, _ := template.New("container-mem-template").Delims("[[", "]]").Parse(containerMemTemplate)
	containerCPUTmpl, _ := template.New("container-cpu-template").Delims("[[", "]]").Parse(containerCPUTemplate)

	newCofingmap := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: configMapName,
		},
		Data: map[string]string{},
	}
	for _, rule := range rules {
		var tpl bytes.Buffer
		if rule.ResourceType == "node" { // ugly
			if rule.Target == "cpu" {
				cpuTmpl.Execute(&tpl, rule)
			} else {
				memTmpl.Execute(&tpl, rule)
			}
		} else if rule.ResourceType == "container" {
			if rule.Target == "cpu" {
				containerCPUTmpl.Execute(&tpl, rule)
			} else {
				containerMemTmpl.Execute(&tpl, rule)
			}
		}

		newCofingmap.Data[rule.ID+".rules"] = tpl.String()
	}

	// 建立 k8s 连接

	config, err2 := clientcmd.BuildConfigFromFlags("", "/Users/fan/k8s-admin.conf") // 集群外
	//config, err2 := rest.InClusterConfig() // 集群内
	if err2 != nil {
		fmt.Println("get error:", err2)
	}
	// create the clientset
	clientset, _ := kubernetes.NewForConfig(config)

	pods, _ := clientset.CoreV1().Pods("").List(metav1.ListOptions{})
	fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))

	// ConfigMaps 是否存在
	_, err4 := clientset.CoreV1().ConfigMaps("monitoring").Get(configMapName, metav1.GetOptions{})
	if err4 != nil { // 没有找到
		fmt.Println("get error:", err4.Error())
		// 创建
		retmap, err := clientset.CoreV1().ConfigMaps("monitoring").Create(&newCofingmap)
		if err != nil {
			fmt.Println("get error:", retmap, err.Error())
		}
	} else { // 更新
		retmap, err := clientset.CoreV1().ConfigMaps("monitoring").Update(&newCofingmap)
		if err != nil {
			fmt.Println("get error:", retmap, err.Error())
		}
	}

}

func responseWithJSON(w http.ResponseWriter, json []byte, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	w.Write(json)
}

func errorWithJSON(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, "{message: %q}", message)
}
