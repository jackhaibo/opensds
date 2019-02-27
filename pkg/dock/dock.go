// Copyright (c) 2017 Huawei Technologies Co., Ltd. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
This module implements the entry into operations of storageDock module.

*/

package dock

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	log "github.com/golang/glog"
	"github.com/opensds/opensds/contrib/connector"
	"github.com/opensds/opensds/contrib/drivers"
	c "github.com/opensds/opensds/pkg/context"
	"github.com/opensds/opensds/pkg/db"
	"github.com/opensds/opensds/pkg/dock/discovery"
	"github.com/opensds/opensds/pkg/model"
	pb "github.com/opensds/opensds/pkg/model/proto"
	"github.com/opensds/opensds/pkg/utils"

	"github.com/opensds/opensds/pkg/utils/constants"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	_ "github.com/opensds/opensds/contrib/connector/fc"
	_ "github.com/opensds/opensds/contrib/connector/iscsi"
	_ "github.com/opensds/opensds/contrib/connector/rbd"
)

// dockServer is used to implement pb.DockServer
type dockServer struct {
	Port string
	// Discoverer represents the mechanism of DockHub discovering the storage
	// capabilities from different backends.
	Discoverer discovery.DockDiscoverer
	// Driver represents the specified backend resource. This field is used
	// for initializing the specified volume driver.
	Driver drivers.VolumeDriver
}

// NewDockServer returns a dockServer instance.
func NewDockServer(dockType, port string) *dockServer {
	return &dockServer{
		Port:       port,
		Discoverer: discovery.NewDockDiscoverer(dockType),
	}
}

// Run method would automatically discover dock and pool resources from
// backends, and then start the listen mechanism of dock module.
func (ds *dockServer) Run() error {
	// New Grpc Server
	s := grpc.NewServer()
	// Register dock service.
	pb.RegisterProvisionDockServer(s, ds)
	pb.RegisterAttachDockServer(s, ds)

	// Trigger the discovery and report loop so that the dock service would
	// update the capabilities from backends automatically.
	if err := func() error {
		var err error
		if err = ds.Discoverer.Init(); err != nil {
			return err
		}
		ctx := &discovery.Context{
			StopChan: make(chan bool),
			ErrChan:  make(chan error),
			MetaChan: make(chan string),
		}
		go discovery.DiscoveryAndReport(ds.Discoverer, ctx)
		go func(ctx *discovery.Context) {
			if err = <-ctx.ErrChan; err != nil {
				log.Error("When calling capabilty report method:", err)
				ctx.StopChan <- true
			}
		}(ctx)
		return err
	}(); err != nil {
		return err
	}

	// Listen the dock server port.
	lis, err := net.Listen("tcp", ds.Port)
	if err != nil {
		log.Fatalf("failed to listen: %+v", err)
		return err
	}

	log.Info("Dock server initialized! Start listening on port:", lis.Addr())

	// Start dock server watching loop.
	defer s.Stop()
	return s.Serve(lis)
}

// CreateVolume implements pb.DockServer.CreateVolume
func (ds *dockServer) CreateVolume(ctx context.Context, opt *pb.CreateVolumeOpts) (*pb.GenericResponse, error) {
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive create volume request, vr =", opt)

	vol, err := ds.Driver.CreateVolume(opt)
	if err != nil {
		log.Error("When create volume in dock module:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(vol), nil
}

// DeleteVolume implements pb.DockServer.DeleteVolume
func (ds *dockServer) DeleteVolume(ctx context.Context, opt *pb.DeleteVolumeOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive delete volume request, vr =", opt)

	if err := ds.Driver.DeleteVolume(opt); err != nil {
		log.Error("Error occurred in dock module when delete volume:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(nil), nil
}

// ExtendVolume implements pb.DockServer.ExtendVolume
func (ds *dockServer) ExtendVolume(ctx context.Context, opt *pb.ExtendVolumeOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive extend volume request, vr =", opt)

	vol, err := ds.Driver.ExtendVolume(opt)
	if err != nil {
		log.Error("When extend volume in dock module:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(vol), nil
}

// CreateVolumeAttachment implements pb.DockServer.CreateVolumeAttachment
func (ds *dockServer) CreateVolumeAttachment(ctx context.Context, opt *pb.CreateVolumeAttachmentOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive create volume attachment request, vr =", opt)

	connInfo, err := ds.Driver.InitializeConnection(opt)
	if err != nil {
		log.Error("Error occurred in dock module when initialize volume connection:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	var atc = &model.VolumeAttachmentSpec{
		BaseModel: &model.BaseModel{
			Id: opt.GetId(),
		},
		VolumeId: opt.GetVolumeId(),
		HostInfo: model.HostInfo{
			Platform:  opt.HostInfo.GetPlatform(),
			OsType:    opt.HostInfo.GetOsType(),
			Ip:        opt.HostInfo.GetIp(),
			Host:      opt.HostInfo.GetHost(),
			Initiator: opt.HostInfo.GetInitiator(),
		},
		ConnectionInfo: *connInfo,
		Metadata:       opt.GetMetadata(),
	}
	return pb.GenericResponseResult(atc), nil
}

// DeleteVolumeAttachment implements pb.DockServer.DeleteVolumeAttachment
func (ds *dockServer) DeleteVolumeAttachment(ctx context.Context, opt *pb.DeleteVolumeAttachmentOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive delete volume attachment request, vr =", opt)

	if err := ds.Driver.TerminateConnection(opt); err != nil {
		log.Error("Error occurred in dock module when terminate volume connection:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(nil), nil
}

// CreateVolumeSnapshot implements pb.DockServer.CreateVolumeSnapshot
func (ds *dockServer) CreateVolumeSnapshot(ctx context.Context, opt *pb.CreateVolumeSnapshotOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive create volume snapshot request, vr =", opt)

	snp, err := ds.Driver.CreateSnapshot(opt)
	if err != nil {
		log.Error("Error occurred in dock module when create snapshot:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(snp), nil
}

// DeleteVolumeSnapshot implements pb.DockServer.DeleteVolumeSnapshot
func (ds *dockServer) DeleteVolumeSnapshot(ctx context.Context, opt *pb.DeleteVolumeSnapshotOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive delete volume snapshot request, vr =", opt)

	if err := ds.Driver.DeleteSnapshot(opt); err != nil {
		log.Error("Error occurred in dock module when delete snapshot:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(nil), nil
}

// AttachVolume implements pb.DockServer.AttachVolume
func (ds *dockServer) AttachVolume(ctx context.Context, opt *pb.AttachVolumeOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	var connData = make(map[string]interface{})
	if err := json.Unmarshal([]byte(opt.GetConnectionData()), &connData); err != nil {
		log.Error("Error occurred in dock module when unmarshalling connection data!")
		return pb.GenericResponseError(err), err
	}

	log.Info("Dock server receive attach volume request, vr =", opt)

	con := connector.NewConnector(opt.GetAccessProtocol())
	if con == nil {
		err := fmt.Errorf("Can not find connector (%s)!", opt.GetAccessProtocol())
		return pb.GenericResponseError(err), err
	}
	atc, err := con.Attach(connData)
	if err != nil {
		log.Error("Error occurred in dock module when attach volume:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(atc), nil
}

// DetachVolume implements pb.DockServer.DetachVolume
func (ds *dockServer) DetachVolume(ctx context.Context, opt *pb.DetachVolumeOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	var connData = make(map[string]interface{})
	if err := json.Unmarshal([]byte(opt.GetConnectionData()), &connData); err != nil {
		log.Error("Error occurred in dock module when unmarshalling connection data!")
		return pb.GenericResponseError(err), err
	}

	log.Info("Dock server receive detach volume request, vr =", opt)

	con := connector.NewConnector(opt.GetAccessProtocol())
	if con == nil {
		err := fmt.Errorf("Can not find connector (%s)!", opt.GetAccessProtocol())
		return pb.GenericResponseError(err), err
	}
	if err := con.Detach(connData); err != nil {
		log.Error("Error occurred in dock module when detach volume:", err)
		return pb.GenericResponseError(err), err
	}
	// TODO: maybe need to update status in DB.
	return pb.GenericResponseResult(nil), nil
}

// CreateReplication implements opensds.DockServer
func (ds *dockServer) CreateReplication(ctx context.Context, opt *pb.CreateReplicationOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage replication drivers and do some initializations.
	driver, _ := drivers.InitReplicationDriver(opt.GetDriverName())
	defer drivers.CleanReplicationDriver(driver)

	log.Info("Dock server receive create replication request, vr =", opt)
	replica, err := driver.CreateReplication(opt)
	if err != nil {
		log.Error("Error occurred in dock module when create replication:", err)
		return pb.GenericResponseError(err), err
	}

	replica.PoolId = opt.GetPoolId()
	replica.ProfileId = opt.GetProfileId()
	replica.Name = opt.GetName()

	return pb.GenericResponseResult(replica), nil
}

func (ds *dockServer) DeleteReplication(ctx context.Context, opt *pb.DeleteReplicationOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage replication drivers and do some initializations.
	driver, _ := drivers.InitReplicationDriver(opt.GetDriverName())
	defer drivers.CleanReplicationDriver(driver)

	log.Info("Dock server receive delete replication request, vr =", opt)

	if err := driver.DeleteReplication(opt); err != nil {
		log.Error("Error occurred in dock module when delete snapshot:", err)
		return pb.GenericResponseError(err), err
	}

	return pb.GenericResponseResult(nil), nil
}

func (ds *dockServer) EnableReplication(ctx context.Context, opt *pb.EnableReplicationOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage replication drivers and do some initializations.
	driver, _ := drivers.InitReplicationDriver(opt.GetDriverName())
	defer drivers.CleanReplicationDriver(driver)

	log.Info("Dock server receive enable replication request, vr =", opt)

	if err := driver.EnableReplication(opt); err != nil {
		log.Error("Error occurred in dock module when enable replication:", err)
		return pb.GenericResponseError(err), err
	}

	return pb.GenericResponseResult(nil), nil
}

func (ds *dockServer) DisableReplication(ctx context.Context, opt *pb.DisableReplicationOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage replication drivers and do some initializations.
	driver, _ := drivers.InitReplicationDriver(opt.GetDriverName())
	defer drivers.CleanReplicationDriver(driver)

	log.Info("Dock server receive disable replication request, vr =", opt)

	if err := driver.DisableReplication(opt); err != nil {
		log.Error("Error occurred in dock module when disable replication:", err)
		return pb.GenericResponseError(err), err
	}

	return pb.GenericResponseResult(nil), nil
}

func (ds *dockServer) FailoverReplication(ctx context.Context, opt *pb.FailoverReplicationOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage replication drivers and do some initializations.
	driver, _ := drivers.InitReplicationDriver(opt.GetDriverName())
	defer drivers.CleanReplicationDriver(driver)

	log.Info("Dock server receive failover replication request, vr =", opt)

	if err := driver.FailoverReplication(opt); err != nil {
		log.Error("Error occurred in dock module when failover replication:", err)
		return pb.GenericResponseError(err), err
	}

	return pb.GenericResponseResult(nil), nil
}

// CreateVolumeGroup implements pb.DockServer.CreateVolumeGroup
func (ds *dockServer) CreateVolumeGroup(ctx context.Context, opt *pb.CreateVolumeGroupOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive create volume group request, vr =", opt)

	// NOTE Opt parameter requires complete volumegroup information, because driver may use it.
	vg, err := db.C.GetVolumeGroup(c.NewContextFromJson(opt.GetContext()), opt.GetId())
	if err != nil {
		return pb.GenericResponseError(err), err
	}

	_, err = ds.Driver.CreateVolumeGroup(opt, vg)
	if err != nil {
		if _, ok := err.(*model.NotImplementError); !ok {
			log.Error("When calling volume driver to create volume group:", err)
			return pb.GenericResponseError(err), err
		}
	}

	log.Info("Create group successfully.")
	return pb.GenericResponseResult(vg), nil
}

func (ds *dockServer) UpdateVolumeGroup(ctx context.Context, opt *pb.UpdateVolumeGroupOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	// Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive update volume group request, vr =", opt)

	add := true
	addVolumesRef, err := ds.getVolumesForGroup(opt, opt.AddVolumes, add)
	if err != nil {
		return pb.GenericResponseError(err), err
	}
	add = false
	removeVolumesRef, err := ds.getVolumesForGroup(opt, opt.RemoveVolumes, add)
	if err != nil {
		return pb.GenericResponseError(err), err
	}

	group, err := db.C.GetVolumeGroup(c.NewContextFromJson(opt.GetContext()), opt.GetId())
	if err != nil {
		return pb.GenericResponseError(err), err
	}

	_, _, _, err := ds.Driver.UpdateVolumeGroup(opt, group, addVolumesRef, removeVolumesRef)
	// Group update faild...

	if err != nil {
		if _, ok := err.(*model.NotImplementError); !ok {
			err = errors.New("Error occurred when updating group" + opt.GetId() + "," + err.Error())
			return pb.GenericResponseError(err), err
		}
	}

	log.Info("Update group successfully.")
	return pb.GenericResponseResult(nil), nil
}

func (ds *dockServer) getVolumesForGroup(opt *pb.UpdateVolumeGroupOpts, volumes []string, add bool) ([]*model.VolumeSpec, error) {
	var volumesRef []*model.VolumeSpec
	for _, v := range volumes {
		vol, err := db.C.GetVolume(c.NewContextFromJson(opt.GetContext()), v)
		if err != nil {
			log.Error("Update group failed", err)
			return nil, err
		}

		if add == true && !utils.Contained(vol.Status, []string{model.VolumeAvailable, model.VolumeInUse}) {
			msg := fmt.Sprintf("Update group failed, wrong status for volume %s %s", vol.Id, vol.Status)
			log.Error(msg)
			return nil, errors.New(msg)
		}

		if add == false && !utils.Contained(vol.Status, []string{model.VolumeAvailable, model.VolumeInUse, model.VolumeError, model.VolumeErrorDeleting}) {
			msg := fmt.Sprintf("Update group failed, wrong status for volume %s %s", vol.Id, vol.Status)
			log.Error(msg)
			return nil, errors.New(msg)
		}
		volumesRef = append(volumesRef, vol)
	}
	return volumesRef, nil
}

func (ds *dockServer) DeleteVolumeGroup(ctx context.Context, opt *pb.DeleteVolumeGroupOpts) (*pb.GenericResponse, error) {
	var res pb.GenericResponse
	//Get the storage drivers and do some initializations.
	ds.Driver = drivers.Init(opt.GetDriverName())
	defer drivers.Clean(ds.Driver)

	log.Info("Dock server receive delete volume group request, vr =", opt)

	volumes, err := db.C.ListVolumesByGroupId(c.NewContextFromJson(opt.GetContext()), opt.GetId())
	if err != nil {
		res.Reply = GenericResponseError("400", fmt.Sprint(err))
		return &res, err
	}

	for _, vol := range volumes {
		if vol.AttachStatus == model.VolumeAttached {
			err = fmt.Errorf("Volume %s is still attached, need to detach first.", vol.Id)
			res.Reply = GenericResponseError("400", fmt.Sprint(err))
			return &res, err
		}
	}

	group, err := db.C.GetVolumeGroup(c.NewContextFromJson(opt.GetContext()), opt.GetId())
	if err != nil {
		res.Reply = GenericResponseError("400", fmt.Sprint(err))
		return &res, err
	}

	var groupUpdate *model.VolumeGroupSpec
	var volumesUpdate *model.VolumeSpec

	groupUpdate, volumesUpdate, err = ds.Driver.DeleteVolumeGroup(opt, group, volumes)

	if err != nil {
		if _, ok := err.(*model.NotImplementError); ok {
			groupUpdate, volumesUpdate = ds.deleteGroupGeneric(ds.Driver, group, volumes, opt)
		} else {
			res.Reply = GenericResponseError("400", fmt.Sprint(err))
		}
	}
	return groupUpdate, volumesUpdate, &res, err

}

func (ds *dockServer) deleteGroupGeneric(driver drivers.VolumeDriver, vg *model.VolumeGroupSpec, volumes []*model.VolumeSpec, opt *pb.DeleteVolumeGroupOpts) (*model.VolumeGroupSpec, []*model.VolumeSpec) {
	//Delete a group and volumes in the group
	var volumesUpdate []*model.VolumeSpec
	vgUpdate := &model.VolumeGroupSpec{
		BaseModel: &model.BaseModel{
			Id: vg.Id,
		},
		Status: vg.Status,
	}

	for _, volumeRef := range volumes {
		v := &model.VolumeSpec{
			BaseModel: &model.BaseModel{
				Id: volumeRef.Id,
			},
		}
		if err := driver.DeleteVolume(&pb.DeleteVolumeOpts{Metadata: volumeRef.Metadata}); err != nil {
			v.Status = model.VolumeError
			vgUpdate.Status = model.VolumeGroupError
			volumesUpdate = append(volumesUpdate, v)
			log.Error(fmt.Sprintf("Error occurred when delete volume %s from group.", volumeRef.Id))
		} else {
			// Delete the volume entry in DB after successfully deleting the volume on the storage.
			if err = db.C.DeleteVolume(c.NewContextFromJson(opt.GetContext()), volumeRef.Id); err != nil {
				log.Errorf("Error occurred in dock module when delete volume %s in db:%v", volumeRef.Id, err)
				vgUpdate.Status = model.VolumeGroupError
			}
		}
	}

	return vgUpdate, volumesUpdate
}
