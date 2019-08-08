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

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	time "time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
	sourcesv1alpha1 "knative.dev/eventing-contrib/kafka/source/pkg/apis/sources/v1alpha1"
	versioned "knative.dev/eventing-contrib/kafka/source/pkg/client/clientset/versioned"
	internalinterfaces "knative.dev/eventing-contrib/kafka/source/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "knative.dev/eventing-contrib/kafka/source/pkg/client/listers/sources/v1alpha1"
)

// KafkaSourceInformer provides access to a shared informer and lister for
// KafkaSources.
type KafkaSourceInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.KafkaSourceLister
}

type kafkaSourceInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewKafkaSourceInformer constructs a new informer for KafkaSource type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewKafkaSourceInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredKafkaSourceInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredKafkaSourceInformer constructs a new informer for KafkaSource type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredKafkaSourceInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SourcesV1alpha1().KafkaSources(namespace).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SourcesV1alpha1().KafkaSources(namespace).Watch(options)
			},
		},
		&sourcesv1alpha1.KafkaSource{},
		resyncPeriod,
		indexers,
	)
}

func (f *kafkaSourceInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredKafkaSourceInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *kafkaSourceInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&sourcesv1alpha1.KafkaSource{}, f.defaultInformer)
}

func (f *kafkaSourceInformer) Lister() v1alpha1.KafkaSourceLister {
	return v1alpha1.NewKafkaSourceLister(f.Informer().GetIndexer())
}
