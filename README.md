# node-stats
Simple command line utility to collect kernel / docker / nftables stats

#Author's note
While building site reliability dashboards for monitoring infrastructure it became readily apparent that very few packages had everything in one place to do full docker / kubernetes monitoring.

But I mean by this is the two biggest packages out in the market are node-exporter and cadvisor.

NE gives you a good view of the health of the underlying bare metal, but it doesn't give you any details of the containers.

CA gives you a view of the containers / kernel namespaces, however it is disorganized because block stats and CPU memory stats both report on the container ID and not the container name.  When building Grafana tables or any monitoring tool it is the best to use named variables instead of IDs because IDs change quickly and cause alerting systems to no longer function properly.


Enter node-stats.  This preprocessing for containers is done on the collection, and the node details is trimmed down to items that are most affecting infrastructure health.
