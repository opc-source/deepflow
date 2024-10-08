/*
 * Copyright (c) 2024 Yunshan Networks
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package kubernetes_gather

import (
	"strconv"
	"strings"

	"github.com/bitly/go-simplejson"
	mapset "github.com/deckarep/golang-set"
	cloudcommon "github.com/deepflowio/deepflow/server/controller/cloud/common"
	"github.com/deepflowio/deepflow/server/controller/cloud/kubernetes_gather/expand"
	"github.com/deepflowio/deepflow/server/controller/cloud/model"
	"github.com/deepflowio/deepflow/server/controller/common"
	"github.com/deepflowio/deepflow/server/libs/logger"
)

func (k *KubernetesGather) getPodServices() (services []model.PodService, servicePorts []model.PodServicePort, podGroupPorts []model.PodGroupPort, network model.Network, subnets []model.Subnet, vinterfaces []model.VInterface, ips []model.IP, err error) {
	log.Debug("get services starting", logger.NewORGPrefix(k.orgID))
	serviceLcuuidToClusterIP := map[string]string{}
	serviceTypes := map[string]int{
		"NodePort":  common.POD_SERVICE_TYPE_NODEPORT,
		"ClusterIP": common.POD_SERVICE_TYPE_CLUSTERIP,
	}
	servicesArrays := [][]string{k.k8sInfo["*v1.Service"]}
	servicesArrays = append(servicesArrays, k.k8sInfo["*v1.ServiceRule"])
	for i, servicesArray := range servicesArrays {
		for _, s := range servicesArray {
			sData, sErr := simplejson.NewJson([]byte(s))
			if sErr != nil {
				err = sErr
				log.Errorf("service initialization simplejson error: (%s)", sErr.Error(), logger.NewORGPrefix(k.orgID))
				return
			}
			metaData, ok := sData.CheckGet("metadata")
			if !ok {
				log.Info("service metadata not found", logger.NewORGPrefix(k.orgID))
				continue
			}
			uID := metaData.Get("uid").MustString()
			if uID == "" {
				log.Info("service uid not found", logger.NewORGPrefix(k.orgID))
				continue
			}
			name := metaData.Get("name").MustString()
			if name == "" {
				log.Infof("service (%s) name not found", uID, logger.NewORGPrefix(k.orgID))
				continue
			}
			namespace := metaData.Get("namespace").MustString()
			namespaceLcuuid, ok := k.namespaceToLcuuid[namespace]
			if !ok {
				log.Infof("service (%s) namespace not found", name, logger.NewORGPrefix(k.orgID))
				continue
			}
			selector := sData.Get("spec").Get("selector").MustMap()
			if len(selector) == 0 {
				log.Infof("service (%s) selector not found", name, logger.NewORGPrefix(k.orgID))
				continue
			}
			selectorSlice := cloudcommon.GenerateCustomTag(selector, nil, 0, ":")
			specTypeString := sData.Get("spec").Get("type").MustString()
			specType, ok := serviceTypes[specTypeString]
			if !ok {
				log.Infof("service (%s) type (%s) not support", name, specTypeString, logger.NewORGPrefix(k.orgID))
				continue
			}
			clusterIP := sData.Get("spec").Get("clusterIP").MustString()
			if clusterIP == "None" {
				clusterIP = ""
			}
			labels := metaData.Get("labels").MustMap()
			switch i {
			case 1:
				if v, ok := labels[cloudcommon.SVC_RULE_RESOURCE_NAME]; ok {
					delete(labels, cloudcommon.SVC_RULE_RESOURCE_NAME)
					labels[cloudcommon.SVC_RULE_RESOURCE_NAME+"_servicerule"] = v
				}
			}

			annotations := metaData.Get("annotations")
			annotationString := expand.GetAnnotation(annotations, k.annotationRegex, k.customTagLenMax)

			uLcuuid := common.IDGenerateUUID(k.orgID, uID)
			service := model.PodService{
				Lcuuid:             uLcuuid,
				Name:               name,
				Label:              k.GetLabel(labels),
				Annotation:         annotationString,
				Type:               specType,
				Selector:           strings.Join(selectorSlice, ", "),
				ServiceClusterIP:   clusterIP,
				PodNamespaceLcuuid: namespaceLcuuid,
				VPCLcuuid:          k.VPCUUID,
				AZLcuuid:           k.azLcuuid,
				RegionLcuuid:       k.RegionUUID,
				PodClusterLcuuid:   k.podClusterLcuuid,
			}
			specPorts := sData.Get("spec").Get("ports")
			var hasPodGroup bool
			for i := range specPorts.MustArray() {
				podGroupLcuuids := mapset.NewSet()
				labels, fErr := metaData.Get("annotations").Get("field.cattle.io/targetWorkloadIds").String()
				if fErr == nil && labels != "[]" && labels != "null" {
					labelArray, lErr := simplejson.NewJson([]byte(labels))
					if lErr != nil {
						log.Infof("service annotation (%s) init json error: (%s)", labels, lErr.Error(), logger.NewORGPrefix(k.orgID))
						continue
					}
					for i := range labelArray.MustArray() {
						if groupLcuuids, ok := k.nsLabelToGroupLcuuids[namespace+labelArray.GetIndex(i).MustString()]; ok {
							if groupLcuuids.Cardinality() > 0 {
								podGroupLcuuids = podGroupLcuuids.Union(groupLcuuids)
							}
						}
					}
				}

				groupLcuuidsList := []mapset.Set{}
				for key, v := range selector {
					vString, ok := v.(string)
					if !ok {
						vString = ""
					}
					nsLabel := namespace + key + "_" + vString
					groupLcuuids, ok := k.nsLabelToGroupLcuuids[nsLabel]
					if !ok {
						continue
					}
					groupLcuuidsList = append(groupLcuuidsList, groupLcuuids)
				}

				// support OpenGaussCluster
				if ogcName, ok := sData.GetPath("spec", "selector").CheckGet("opengauss.cluster"); ok {
					nsLabel := namespace + "statefulset:" + namespace + ":" + ogcName.MustString()
					if groupLcuuids, ok := k.nsLabelToGroupLcuuids[nsLabel]; ok {
						if groupLcuuids.Cardinality() > 0 {
							podGroupLcuuids = podGroupLcuuids.Union(groupLcuuids)
						}
					}
				} else {
					// 如果存在label匹配不到PodGroup，则认为找不到匹配的PodGroup
					if len(groupLcuuidsList) != len(selector) {
						continue
					}
				}

				// 各Label的PodGroup求交集，作为service关联的PodGroup
				intersectGroupLcuuids := mapset.NewSet()
				if len(groupLcuuidsList) == 1 {
					intersectGroupLcuuids = groupLcuuidsList[0]
				} else if len(groupLcuuidsList) > 1 {
					intersectGroupLcuuids = groupLcuuidsList[0]
					for _, lcuuids := range groupLcuuidsList[1:] {
						intersectGroupLcuuids = intersectGroupLcuuids.Intersect(lcuuids)
					}
				}
				if intersectGroupLcuuids.Cardinality() > 0 {
					podGroupLcuuids = podGroupLcuuids.Union(intersectGroupLcuuids)
				}
				// 如果没有找到关联PodGroup，进入下一循环
				if podGroupLcuuids.Cardinality() == 0 {
					log.Infof("service (%s) pod group id not found", name, logger.NewORGPrefix(k.orgID))
					continue
				}
				hasPodGroup = true

				podTargetPorts := map[string]int{}
				for _, pgLcuuid := range podGroupLcuuids.ToSlice() {
					pgLcuuidString, ok := pgLcuuid.(string)
					if !ok {
						log.Warningf("sercice (%s) pod group lcuuid interface conversion failed", name, logger.NewORGPrefix(k.orgID))
						continue
					}
					targetPorts, ok := k.pgLcuuidTopodTargetPorts[pgLcuuidString]
					if !ok {
						continue
					}
					for name, port := range targetPorts {
						podTargetPorts[name] = port
					}
				}
				ports := specPorts.GetIndex(i)
				var targetPort int
				if targetPortString := ports.Get("targetPort").MustString(); targetPortString != "" {
					targetPort = podTargetPorts[targetPortString]
				}
				if targetPort == 0 {
					targetPort = ports.Get("targetPort").MustInt()
					if targetPort == 0 {
						log.Infof("service (%s) target_port not match", name, logger.NewORGPrefix(k.orgID))
						continue
					}
				}
				nameToPort := map[string]int{}
				nameToPort[ports.Get("name").MustString()] = ports.Get("port").MustInt()
				uidToName := map[string]map[string]int{}
				uidToName[uLcuuid] = nameToPort
				k.nsServiceNameToService[namespace+name] = uidToName
				key := strconv.Itoa(ports.Get("port").MustInt()) + ports.Get("protocol").MustString() + strconv.Itoa(ports.Get("nodePort").MustInt()) + strconv.Itoa(targetPort)
				servicePort := model.PodServicePort{
					Lcuuid:           common.GetUUIDByOrgID(k.orgID, uLcuuid+key),
					Name:             ports.Get("name").MustString(),
					Protocol:         strings.ToUpper(ports.Get("protocol").MustString()),
					Port:             ports.Get("port").MustInt(),
					TargetPort:       targetPort,
					NodePort:         ports.Get("nodePort").MustInt(),
					PodServiceLcuuid: uLcuuid,
				}

				// 在service确定有pod group的时候添加pod service port
				servicePorts = append(servicePorts, servicePort)
				for _, Lcuuid := range podGroupLcuuids.ToSlice() {
					pgLcuuid, ok := Lcuuid.(string)
					if !ok {
						log.Warningf("pod group lcuuid (%v) assert failed", Lcuuid, logger.NewORGPrefix(k.orgID))
						continue
					}
					key := ports.Get("protocol").MustString() + strconv.Itoa(targetPort)
					podGroupPort := model.PodGroupPort{
						Lcuuid:           common.GetUUIDByOrgID(k.orgID, uLcuuid+pgLcuuid+key),
						Name:             ports.Get("name").MustString(),
						Port:             targetPort,
						Protocol:         strings.ToUpper(ports.Get("protocol").MustString()),
						PodGroupLcuuid:   pgLcuuid,
						PodServiceLcuuid: uLcuuid,
					}
					podGroupPorts = append(podGroupPorts, podGroupPort)
					k.pgLcuuidToPSLcuuids[pgLcuuid] = append(k.pgLcuuidToPSLcuuids[pgLcuuid], uLcuuid)
				}
			}
			if !hasPodGroup {
				delete(k.nsServiceNameToService, namespace+name)
				log.Infof("service (%s) pod group not found", name, logger.NewORGPrefix(k.orgID))
				continue
			}
			services = append(services, service)
			if clusterIP != "" && clusterIP != "None" {
				serviceLcuuidToClusterIP[uLcuuid] = clusterIP
			}
		}
	}

	serviceNetworkName := k.Name + "_SVC_NET"
	serviceNetworkLcuuid := common.GetUUIDByOrgID(k.orgID, k.UuidGenerate+serviceNetworkName)
	clusterIPs := cloudcommon.StringStringMapValues(serviceLcuuidToClusterIP)
	serviceCIDR := []string{}
	if len(clusterIPs) != 0 {
		v4Prefixs, v6Prefixs, tErr := cloudcommon.TidyIPString(clusterIPs)
		if tErr != nil {
			err = tErr
			log.Error("service tidy cluster ip Error"+tErr.Error(), logger.NewORGPrefix(k.orgID))
			return
		}
		if len(v4Prefixs) != 0 {
			v4cidrs := cloudcommon.AggregateCIDR(v4Prefixs, 0)
			serviceCIDR = append(serviceCIDR, v4cidrs...)
		}
		if len(v6Prefixs) != 0 {
			v6cidrs := cloudcommon.AggregateCIDR(v6Prefixs, 0)
			serviceCIDR = append(serviceCIDR, v6cidrs...)
		}
	}

	serviceSubnetLcuuid := common.GetUUIDByOrgID(k.orgID, serviceNetworkLcuuid)
	for i, sCIDR := range serviceCIDR {
		if i > 1 {
			serviceSubnetLcuuid = common.GetUUIDByOrgID(k.orgID, serviceNetworkLcuuid+sCIDR)
		}
		nodeSubnet := model.Subnet{
			Lcuuid:        serviceSubnetLcuuid,
			Name:          serviceNetworkName,
			CIDR:          sCIDR,
			NetworkLcuuid: serviceNetworkLcuuid,
			VPCLcuuid:     k.VPCUUID,
		}
		subnets = append(subnets, nodeSubnet)
	}

	network = model.Network{
		Lcuuid:         serviceNetworkLcuuid,
		Name:           serviceNetworkName,
		SegmentationID: 1,
		Shared:         false,
		External:       false,
		NetType:        common.NETWORK_TYPE_LAN,
		AZLcuuid:       k.azLcuuid,
		VPCLcuuid:      k.VPCUUID,
		RegionLcuuid:   k.RegionUUID,
	}
	for Lcuuid, IP := range serviceLcuuidToClusterIP {
		vinterfaceID := common.GetUUIDByOrgID(k.orgID, Lcuuid+common.VIF_DEFAULT_MAC+IP)
		vinterface := model.VInterface{
			Lcuuid:        vinterfaceID,
			Type:          common.VIF_TYPE_LAN,
			Mac:           common.VIF_DEFAULT_MAC,
			DeviceLcuuid:  Lcuuid,
			DeviceType:    common.VIF_DEVICE_TYPE_POD_SERVICE,
			NetworkLcuuid: serviceNetworkLcuuid,
			VPCLcuuid:     k.VPCUUID,
			RegionLcuuid:  k.RegionUUID,
		}
		vinterfaces = append(vinterfaces, vinterface)
		ip := model.IP{
			Lcuuid:           common.GetUUIDByOrgID(k.orgID, Lcuuid+IP),
			VInterfaceLcuuid: vinterfaceID,
			IP:               IP,
			RegionLcuuid:     k.RegionUUID,
			SubnetLcuuid:     common.GetUUIDByOrgID(k.orgID, serviceNetworkLcuuid),
		}
		ips = append(ips, ip)
	}
	log.Debug("get services complete", logger.NewORGPrefix(k.orgID))
	return
}
