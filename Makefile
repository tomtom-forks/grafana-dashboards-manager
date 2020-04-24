OS=linux
ARCH=amd64
RELEASE=$$(git rev-parse HEAD)

default: bin

clean:
	rm -rf bin/

bin:
	mkdir -p bin
	cd cmd && for dir in *; do cd $$dir && go build -i -o ../../bin/$$dir && cd ..; done

format fmt:
	go fmt -x ./...
