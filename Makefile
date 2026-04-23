build:
	mkdir -p bin && \
	export CGO_ENABLED=0; \
	export GOOS=linux; \
	export GOARCH=amd64; \
	go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o bin/auth-server ./services/auth/cmd/server

run:
	./bin/auth-server

POSTGRES_SH := bash src/core/postgres_cluster.sh

.PHONY: pg-cluster pg-backup pg-restore-latest pg-restore-time

pg-cluster:
	$(POSTGRES_SH) deploy --create-initial-backup false

pg-backup:
	$(POSTGRES_SH) backup --wait

pg-restore-latest:
	$(POSTGRES_SH) deploy --restore latest --force-recreate

pg-restore-time:
	@test -n "$$TARGET_TIME" || (echo "ERROR: TARGET_TIME must be set (RFC3339)" && exit 1)
	$(POSTGRES_SH) deploy --restore time --target-time "$$TARGET_TIME" --force-recreate

set-staging-eks-context:
	./src/scripts/set_k8s_context.sh staging

set-prod-eks-context:
	./src/scripts/set_k8s_context.sh prod

set-kind-context:
	kubectl config use-context kind-rag8s-local

push-frontend:
	ruff check src/services/frontend/ --fix
	git add .github/workflows/ src/services/frontend/
	gitleaks detect --source src/services/frontend/ --no-git --exit-code 1
	git commit -m "updating nginx SPA image"
	git push origin main

push-all:
	git add .
	git commit -m "new"
	git push origin main --force


temp-s3:
	ACCOUNT_ID=$$(aws sts get-caller-identity --query Account --output text); \
	BUCKET=s3-temp-bucket-mlsecops-$$ACCOUNT_ID; \
	REGION=$$AWS_REGION; \
	if ! aws s3api head-bucket --bucket $$BUCKET 2>/dev/null; then \
		aws s3api create-bucket \
			--bucket $$BUCKET \
			--region $$REGION \
			--create-bucket-configuration LocationConstraint=$$REGION; \
		echo "Created $$BUCKET"; \
	else \
		echo "Bucket $$BUCKET already exists"; \
	fi

delete-temp-s3:
	ACCOUNT_ID=$$(aws sts get-caller-identity --query Account --output text); \
	BUCKET=s3-temp-bucket-mlsecops-$$ACCOUNT_ID; \
	REGION=$$AWS_REGION; \
	if aws s3api head-bucket --bucket $$BUCKET 2>/dev/null; then \
		aws s3 rm s3://$$BUCKET --recursive; \
		aws s3api delete-bucket --bucket $$BUCKET --region $$REGION; \
		echo "Deleted $$BUCKET"; \
	else \
		echo "Bucket $$BUCKET does not exist"; \
	fi


core:
	kind delete cluster --name local-cluster || true && kind create cluster --name local-cluster && \
	bash src/infra/core/default_storage_class.sh

tree:
	tree -a -I '.git|.venv|archive|__pycache__|.repos|tmp.md|.ruff_cache'

push:
	git add .
	git commit -m "new"
	gitleaks detect --source . --exit-code 1 --redact
	git push origin main --force

clean:
	find . -type d -name "__pycache__" -exec rm -rf {} +
	find . -type f -name "*.pyc" -delete
	find . -type f -name "*.log" ! -path "./.git/*" -delete
	find . -type f -name "*.pulumi-logs" ! -path "./.git/*" -delete
	find . -type d -name ".ruff_cache" -exec rm -rf {} +
	rm -rf logs
	rm -rf src/terraform/.plans
	clear

iac-staging:
	bash src/terraform/aws/run.sh --create --env staging || true
delete-iac-staging:
	bash src/terraform/aws/run.sh --delete --yes-delete --env staging

test-iac-staging:
	bash src/terraform/aws/run.sh --create --env staging || true && \
	bash src/terraform/aws/run.sh --delete --yes-delete --env staging

sync:
	aws s3 sync s3://$$S3_BUCKET/iceberg/warehouse/ $(pwd)/data/iceberg/
