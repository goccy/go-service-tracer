# go-service-tracer
Visualize the dependencies between Microservices of gRPC methods implemented in Go

# Installation

```
go get github.com/goccy/go-service-tracer/cmd/go-service-tracer
```

# Usage

Consider visualizing the dependencies between microservices A and B.

## Microservice A

The repository is developed at github.com/organization/service-a .
The `main` package, which is used to start the server, is located under `cmd/a` .
In order to allow other services to call the API of Microservice A, the proto files maintain at github.com/organization/proto .
proto files are located in `service-a/v1` directory in github.com/organization/proto ( github.com/organization/proto/service-a/v1 ) .

## Microservice B

The repository is developed at github.com/organization/service-b .
The `main` package, which is used to start the server, is located under `cmd/b` .
In order to allow other services to call the API of Microservice B, the proto files maintain at github.com/organization/proto .
proto files are located in `service-b/v1` directory in github.com/organization/proto ( github.com/organization/proto/service-b/v1 ) .


## Getting Started

### Write service definition

Write sevice definition to the `trace.yaml` .

```yaml
auth:
  token:
    env: GITHUB_TOKEN
services:
  - name: serviceA
    repo: github.com/organization/service-a
    entry: cmd/a
    proto:
      repo: github.com/organization/proto
      path:
        - service-a/v1
  - name: serviceB
    repo: github.com/organization/service-b
    entry: cmd/b
    proto:
      repo: github.com/organization/proto
      path:
        - service-b/v1
```

`auth.token.env` parameter available access to private repository.
At this example set token to access to private repository as `GITHUB_TOKEN` .

### Run go-service-tracer

Install `go-service-tracer` and run the following command .

```
go-service-tracer -c trace.yaml
```

On success, `trace.html` is generated in the current directory.

