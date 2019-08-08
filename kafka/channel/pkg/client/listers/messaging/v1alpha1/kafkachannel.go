/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	v1alpha1 "knative.dev/eventing-contrib/kafka/channel/pkg/apis/messaging/v1alpha1"
)

// KafkaChannelLister helps list KafkaChannels.
type KafkaChannelLister interface {
	// List lists all KafkaChannels in the indexer.
	List(selector labels.Selector) (ret []*v1alpha1.KafkaChannel, err error)
	// KafkaChannels returns an object that can list and get KafkaChannels.
	KafkaChannels(namespace string) KafkaChannelNamespaceLister
	KafkaChannelListerExpansion
}

// kafkaChannelLister implements the KafkaChannelLister interface.
type kafkaChannelLister struct {
	indexer cache.Indexer
}

// NewKafkaChannelLister returns a new KafkaChannelLister.
func NewKafkaChannelLister(indexer cache.Indexer) KafkaChannelLister {
	return &kafkaChannelLister{indexer: indexer}
}

// List lists all KafkaChannels in the indexer.
func (s *kafkaChannelLister) List(selector labels.Selector) (ret []*v1alpha1.KafkaChannel, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.KafkaChannel))
	})
	return ret, err
}

// KafkaChannels returns an object that can list and get KafkaChannels.
func (s *kafkaChannelLister) KafkaChannels(namespace string) KafkaChannelNamespaceLister {
	return kafkaChannelNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// KafkaChannelNamespaceLister helps list and get KafkaChannels.
type KafkaChannelNamespaceLister interface {
	// List lists all KafkaChannels in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1alpha1.KafkaChannel, err error)
	// Get retrieves the KafkaChannel from the indexer for a given namespace and name.
	Get(name string) (*v1alpha1.KafkaChannel, error)
	KafkaChannelNamespaceListerExpansion
}

// kafkaChannelNamespaceLister implements the KafkaChannelNamespaceLister
// interface.
type kafkaChannelNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all KafkaChannels in the indexer for a given namespace.
func (s kafkaChannelNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.KafkaChannel, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.KafkaChannel))
	})
	return ret, err
}

// Get retrieves the KafkaChannel from the indexer for a given namespace and name.
func (s kafkaChannelNamespaceLister) Get(name string) (*v1alpha1.KafkaChannel, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("kafkachannel"), name)
	}
	return obj.(*v1alpha1.KafkaChannel), nil
}
