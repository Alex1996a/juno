package grpcinvoker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bojand/ghz/protodesc"
	"github.com/douyu/jupiter/pkg/xlog"
	"github.com/golang/protobuf/jsonpb"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var (
	instance *ProtoCmd
	initOnce sync.Once
)

type ProtoCmd struct {
}

func Instance() *ProtoCmd {
	initOnce.Do(func() {
		instance = &ProtoCmd{}
	})
	return instance
}

//  ./grpctest --ProtoFile=aggregation.ProtoFile --ckall=pb.Aggregation.BatchRoomInfo --m='{"Aid":"1"}' --d='{"Rids":[20]}' --Host=10.1.41.166:50127
type ReqProtoConfig struct {
	PackageName string
	ServiceName string
	MethodName  string
	call        string
	InputParams string
	MetaData    string
	ProtoFile   string
	Host        string
	Timeout     time.Duration
}

func MakeRequest(r ReqProtoConfig) (resp *dynamic.Message, err error) {
	// 判断proto是否存在
	_, err = os.Stat(r.ProtoFile)
	if err != nil {
		err = errors.New("file path is error, err: " + err.Error())
		return
	}
	r.call = r.PackageName + "." + r.ServiceName + "." + r.MethodName
	mtd, err := protoData(r.call, r.ProtoFile)
	if err != nil {
		return
	}

	xlog.Info("request", xlog.String("call", r.call), xlog.String("protoFile", r.ProtoFile),
		xlog.String("inputParam", r.InputParams))

	ctd := newCallTemplateData(mtd)
	inputs, err := getMessages(ctd, r.InputParams, mtd)
	if err != nil {
		return
	}

	mdMap, err := ctd.executeMetadata(string(r.MetaData))
	if err != nil {
		return
	}

	var reqMD *metadata.MD
	if mdMap != nil && len(*mdMap) > 0 {
		md := metadata.New(*mdMap)
		reqMD = &md
	}

	ctx := context.Background()
	var cancel context.CancelFunc

	if r.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// include the metadata
	if reqMD != nil {
		ctx = metadata.NewOutgoingContext(ctx, *reqMD)
	}

	inputsLen := len(*inputs)
	if inputsLen == 0 {
		err = fmt.Errorf("no data provided for request")
		return
	}

	conn, err := getGrpcConn(r.Host)
	if err != nil {
		err = fmt.Errorf("grpc conn fail:" + err.Error())
		return
	}
	respInterface, err := grpcdynamic.NewStub(conn).InvokeRpc(ctx, mtd, (*inputs)[0])
	if err != nil {
		return
	}
	if respInterface != nil {
		resp = respInterface.(*dynamic.Message)
	}
	return
}

func protoData(call string, proto string) (mtd *desc.MethodDescriptor, err error) {
	var importPaths []string

	dir := filepath.Dir(proto)
	if dir != "." {
		importPaths = append(importPaths, dir)
	}

	mtd, err = protodesc.GetMethodDescFromProto(call, proto, importPaths)
	if err != nil {
		return
	}

	md := mtd.GetInputType()
	payloadMessage := dynamic.NewMessage(md)
	if payloadMessage == nil {
		err = errors.New("payload message is nil")
		return
	}

	return
}

func getGrpcConn(host string) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	ctx, _ := context.WithTimeout(context.Background(), 3*time.Second)
	//opts = append(opts, grpc.WithStatsHandler(&statsHandler{b.results}))

	return grpc.DialContext(ctx, host, opts...)
}

func getMessages(ctd *callTemplateData, callData string, mtd *desc.MethodDescriptor) (*[]*dynamic.Message, error) {
	var inputs *[]*dynamic.Message

	strData := string(callData)
	data, err := ctd.executeData(strData)
	if err != nil {
		return nil, err
	}
	//fmt.Println(string(data), 2)
	inputs, err = createPayloadsFromJSON(string(data), mtd)
	if err != nil {
		return nil, err
	}
	// Json messages are not cached due to templating

	return inputs, nil
}

func createPayloadsFromJSON(data string, mtd *desc.MethodDescriptor) (*[]*dynamic.Message, error) {
	md := mtd.GetInputType()
	var inputs []*dynamic.Message

	if len(data) > 0 {
		if strings.IndexRune(data, '[') == 0 {
			dataArray := make([]map[string]interface{}, 5)
			err := json.Unmarshal([]byte(data), &dataArray)
			if err != nil {
				return nil, fmt.Errorf("Error unmarshalling payload. Data: '%v' Error: %v", data, err.Error())
			}

			elems := len(dataArray)
			if elems > 0 {
				inputs = make([]*dynamic.Message, elems)
			}

			for i, elem := range dataArray {
				elemMsg := dynamic.NewMessage(md)
				err := messageFromMap(elemMsg, &elem)
				if err != nil {
					return nil, fmt.Errorf("Error creating message: %v", err.Error())
				}

				inputs[i] = elemMsg
			}
		} else {
			inputs = make([]*dynamic.Message, 1)
			inputs[0] = dynamic.NewMessage(md)
			err := jsonpb.UnmarshalString(data, inputs[0])
			if err != nil {
				// 说明入参和proto的结构体参数不一致
				return nil, fmt.Errorf("Error creating message from data. Data: '%v' Error: %v", data, err.Error())
			}
		}
	}

	return &inputs, nil
}

func messageFromMap(input *dynamic.Message, data *map[string]interface{}) error {
	strData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	err = jsonpb.UnmarshalString(string(strData), input)
	if err != nil {
		return err
	}

	return nil
}
