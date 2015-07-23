default: builddocker

setup:
	echo "Here grab dependencies with 'go get...'"
	# go get ...

buildgo:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-s" -a -installsuffix cgo -o main ./go/src/bitbucket.org/durdn/go-static-build-example/project-name

builddocker:
	docker build -t durdn/build-project-name -f ./Dockerfile.build .
	docker run -t durdn/build-project-name /bin/true
	docker cp `docker ps -q -n=1`:/main .
	docker build --rm=true --tag=durdn/project-name -f Dockerfile.static .

run: builddocker
	docker run \
		-p 8080:8080 durdn/project-name
