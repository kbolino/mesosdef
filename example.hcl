mesos {
    zookeepers = "zk://shepherd1:2181,shepherd2:2181,shepherd3:2181/mesos"
    masters = ["shepherd1:5050", "shepherd2:5050", "shepherd3:5050"]
}

framework "marathon" "default" {
    mesos_name = "marathon"
    masters = ["shepherd1:8080", "shepherd2:8080", "shepherd3:8080"]
}

framework "chronos" "default" {
    mesos_name = "chronos"
    masters = ["chronos.marathon.${dns_tld}:4400"]

    created_by_deployment {
        type = "marathon_app"
        name = "chronos"
    }
}

deployment "marathon_app" "mesos_dns" {
    deploy = "${deploy_root}/core/mesos-dns.json"
    labels = ["bootstrap"]

    dependency_of {
        type = "*"
        wait_for_healthy = true
        
        filter {
            key = "labels"
            value = "bootstrap"
            negate = true
        }
    }
}

deployment "marathon_app" "nexus" {
    deploy = "${deploy_root}/core/nexus.json"
    labels = ["bootstrap"]

    dependency_of {
        type = "marathon_app"
        wait_for_healthy = true

        filter {
            key = "labels"
            value = "bootstrap"
            negate = true
        }
    }
}

deployment "marathon_app" "chronos" {
    deploy = "${deploy_root}/core/chronos.json"
    labels = ["core"]

    dependency_of {
        type = "chronos_job"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "elasticsearch" {
    deploy = "${deploy_root}/monitoring/kibana.json"
    labels = ["monitoring"]
}

deployment "marathon_app" "kibana" {
    deploy = "${deploy_root}/monitoring/kibana.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "elasticsearch"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "logstash" {
    deploy = "${deploy_root}/monitoring/logstash.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "elasticsearch"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "filebeat" {
    deploy = "${deploy_root}/monitoring/filebeat.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "logstash"
        wait_for_healthy = true
    }

    dependency_of {
        type = "*"
        wait_for_healthy = true

        filter {
            key = "labels"
            values = ["bootstrap", "monitoring"]
            negate = true
        }
    }
}

deployment "marathon_app" "influxdb" {
    deploy = "${deploy_root}/monitoring/influxdb.json"
    labels = ["monitoring"]
}

deployment "marathon_app" "grafana" {
    deploy = "${deploy_root}/monitoring/grafana.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "influxdb"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "telegraf" {
    deploy = "${deploy_root}/monitoring/telegraf.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "influxdb"
        wait_for_healthy = true
    }

    dependency_of {
        type = "marathon_app"
        wait_for_healthy = true

        filter {
            key = "labels"
            values = ["bootstrap", "monitoring"]
            negate = true
        }
    }
}

deployment "marathon_app" "kapacitor" {
    deploy = "${deploy_root}/monitoring/kapacitor.json"
    labels = ["monitoring"]

    dependency {
        type = "marathon_app"
        name = "influxdb"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "rabbitmq" {
    deploy = "${deploy_root}/infrastructure/rabbitmq.json"

    dependency_of {
        type = "*"
        wait_for_healthy = true

        filter {
            key = "labels"
            value = "needs_rabbitmq"
        }
    }
}

deployment "marathon_app" "minio" {
    deploy = "${deploy_root}/infrastructure/minio.json"

    dependency_of {
        type = "*"
        wait_for_healthy = true

        filter {
            key = "labels"
            value = "needs_minio"
        }
    }
}

deployment "marathon_app" "nixy" {
    deploy = "${deploy_root}/infrastructure/nixy.json"

    dependency_of {
        type = "*"
        wait_for_healthy = true

        filter {
            key = "labels"
            value = "needs_nixy"
        }
    }
}

deployment "marathon_app" "public_proxy" {
    deploy = "${deploy_root}/public/public-proxy.json"

    dependency_of {
        type = "*"
        wait_for_healthy = true

        filter {
            key = "labels"
            values = ["needs_reverse_proxy", "needs_forward_proxy"]
        }
    }
}

deployment "marathon_app" "web_mysql" {
    deploy = "${deploy_root}/web/mysql.json"
}

deployment "marathon_app" "web_api_v2" {
    deploy = "${deploy_root}/web/api-v2.json"
    labels = ["needs_reverse_proxy", "needs_forward_proxy"]

    dependency {
        type = "marathon_app"
        name = "web_mysql"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "file_handler" {
    deploy = "${deploy_root}/data/file-handler.json"
    labels = ["needs_rabbitmq", "needs_minio"]
}

deployment "marathon_app" "analytic_service" {
    deploy = "${deploy_root}/data/analytic-service.json"
    labels = ["needs_nixy"]
}

deployment "marathon_app" "analytic" {
    deploy = "${deploy_root}/data/analytic.json"
    labels = ["needs_rabbitmq", "needs_minio", "needs_nixy"]

    dependency {
        type = "marathon_app"
        name = "analyic_service"
        wait_for_healthy = true
    }
}

deployment "marathon_app" "analytic_indexer" {
    deploy = "${deploy_root}/data/analytic-indexer.json"
    labels = ["needs_rabbitmq", "needs_minio"]
}

deployment "chronos_job" "minio_cleanup" {
    deploy = "${deploy_root}/data/minio-cleanup.json"
    labels = ["needs_minio"]
}

deployment "chronos_job" "analytic_model_pull" {
    deploy = "${deploy_root}/data/analytic-model-pull.json"
    labels = ["needs_forward_proxy"]
}