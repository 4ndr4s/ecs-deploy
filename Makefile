BINARY = ecs-deploy
GOARCH = amd64

all: build

build:
	GOOS=linux GOARCH=${GOARCH} go build ${LDFLAGS} -o ${BINARY}-linux-${GOARCH} . ; 

test:
	go test

integrationTest:
	export $$(cat .env | grep -v '^\#' | xargs) && go test -run Integration
	
clean:
	rm -f ${BINARY}-linux-${GOARCH}
