echo Installing Web Console...
go get golang.org/x/crypto/bcrypt
go build webconsole.go
cp webconsole /usr/local/bin
