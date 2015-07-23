# Build minimal static Go application with Docker on OSX

[See article](https://developer.atlassian.com/blog/2015/07/osx-static-golang-binaries-with-docker/)

## How to use it

- Install Docker.
- Install a build environment that can run `make`.
- Type: `make builddocker`.
- Run test with: `docker run -t durdn/project-name`
