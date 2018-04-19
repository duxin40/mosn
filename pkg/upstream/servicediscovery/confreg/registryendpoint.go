package registry

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "net/http"
    "gitlab.alipay-inc.com/afe/mosn/pkg/log"
    "gitlab.alipay-inc.com/afe/mosn/pkg/upstream/servicediscovery/confreg/servermanager"
    "github.com/julienschmidt/httprouter"
    "time"
)

type RegistryEndpoint struct {
    ServiceInfo
    MsgChannelCB   MsgChanCallback
    registryClient RegistryClient
}

type MsgChanCallback func([]string)

type ServiceInfo struct {
    ServiceSet []string `json:"service_set"`
}

var subscribeRecorder = make(map[string]chan bool)
var rpcServerChangeListener servermanager.RPCServerChangeListener

type StartupRPCServerChangeListener struct {
}

func (l *StartupRPCServerChangeListener) OnRPCServerChanged(dataId string, zoneServers map[string][]string) {
    if r, ok := subscribeRecorder[dataId]; ok {
        r <- true
    }
}

func NewRegistryEndpoint(services []string, msgChannelCB MsgChanCallback, registryClient RegistryClient) *RegistryEndpoint {
    rpcServerChangeListener = &StartupRPCServerChangeListener{}
    registryClient.GetRPCServerManager().RegisterRPCServerChangeListener(rpcServerChangeListener)
    return &RegistryEndpoint{
        ServiceInfo:    ServiceInfo{ServiceSet: services},
        MsgChannelCB:   msgChannelCB,
        registryClient: registryClient,
    }
}

func (re *RegistryEndpoint) PublishService(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    raw, _ := ioutil.ReadAll(r.Body)

    var request PublishServiceRequest
    if err := json.Unmarshal(raw, &request); err != nil {
        doResponse(false, "Unmarshal body stream to publish service request failed.", w)
        return
    }
    dataId := ps.ByName("serviceName")
    //todo Assemble publish data
    err := re.registryClient.PublishSync(dataId, "127.0.0.1:6666")
    if err == nil {
        doResponse(true, "success", w)
    } else {
        doResponse(false, err, w)
    }
}

func (re *RegistryEndpoint) UnPublishService(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    body, _ := ioutil.ReadAll(r.Body)

    var request UnPublishServiceRequest
    if err := json.Unmarshal(body, &request); err != nil {
        doResponse(false, "Unmarshal body stream to unpublish service request failed.", w)
        return
    }
    err := re.registryClient.UnPublishSync(request.ServiceName)
    if err == nil {
        doResponse(true, "success", w)
    } else {
        doResponse(false, err, w)
    }
}

func (re *RegistryEndpoint) SubscribeService(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    body, _ := ioutil.ReadAll(r.Body)

    var request SubscribeServiceRequest
    if err := json.Unmarshal(body, &request); err != nil {
        doResponse(false, "Unmarshal body stream to subscribe service request failed.", w)
        return
    }
    //1. Subscribe from confreg
    dataId := request.ServiceName
    err := re.registryClient.SubscribeSync(dataId)
    if err != nil {
        doResponse(false, err, w)
        return
    }
    //2. Get service info from confreg in block.
    subscribeRecorder[dataId] = make(chan bool)
    timeout := 3 * time.Second
    t := time.NewTimer(timeout)
    for ; ; {
        select {
        case <-t.C:
            {
                doResponse(false, fmt.Sprintf("Wait confreg server push data timeout. data id = %s, timeout = %v", dataId, timeout), w)
                return
            }
        case <-subscribeRecorder[dataId]:
            {
                result := &SubscribeServiceResult{
                    ErrorMessage: "",
                    Success:      true,
                    ServiceName:  dataId,
                }
                servers, ok := re.registryClient.GetRPCServerManager().GetRPCServerList(dataId)
                if !ok {
                    result.Datas = []string{}
                } else {
                    for _, v := range servers {
                        result.Datas = v
                        break
                    }
                }
                res, _ := json.Marshal(result)
                w.Write(res)
                return
            }

        }
    }
}

func (re *RegistryEndpoint) UnSubscribeService(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    body, _ := ioutil.ReadAll(r.Body)

    var request UnSubscribeServiceRequest
    if err := json.Unmarshal(body, &request); err != nil {
        doResponse(false, "Unmarshal body stream to subscribe service request failed.", w)
        return
    }
    err := re.registryClient.UnSubscribeSync(request.ServiceName)
    if err == nil {
        doResponse(true, "success", w)
    } else {
        doResponse(false, err, w)
    }
}

func (re *RegistryEndpoint) GetServiceInfoSnapshot(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    w.Write(re.registryClient.GetRPCServerManager().GetRPCServiceSnapshot())
}

func doResponse(success bool, errMsg interface{}, w http.ResponseWriter) {
    if !success {
        log.DefaultLogger.Errorf("Handle http request failed. error message = %v", errMsg)
    }
    r := &HttpResponse{
        Success:      success,
        ErrorMessage: errMsg,
    }

    bytes, _ := json.Marshal(r)
    w.Write(bytes)
}

func (re *RegistryEndpoint) StartListener() {
    router := httprouter.New()
    router.POST("/services/publish", re.PublishService)
    router.POST("/services/unpublish", re.UnPublishService)
    router.POST("/services/subscribe", re.SubscribeService)
    router.POST("/services/unsubscribe", re.UnSubscribeService)
    router.GET("/services", re.GetServiceInfoSnapshot)

    port := "8888"
    httpServerEndpoint := "localhost:" + port
    if err := http.ListenAndServe(httpServerEndpoint, router); err != nil {
        log.DefaultLogger.Fatal("Http server startup failed. port = ", port)
    } else {
        log.DefaultLogger.Infof("Http server startup at " + port)
    }
}
