echo build
GOOS=linux go build main.go

echo zip
zip function.zip main temp

echo delete
aws lambda delete-function --region eu-west-1 --function-name my-function

echo upload
aws lambda create-function --region eu-west-1 --function-name my-function --runtime go1.x \
	--timeout 900 \
	--memory-size 1024 \
	--environment Variables=\{LAMBDA=true\} \
	--zip-file fileb://function.zip --handler main \
	--role arn:aws:iam::535282574996:role/dronecon-lambda-role