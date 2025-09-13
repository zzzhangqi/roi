#!/bin/bash

IMAGE_DOMAIN=registry.cn-hangzhou.aliyuncs.com
IMAGE_NAMESPACE=goodrain
VERSION=v6.3.2-release

# Accept architecture from environment variable, fallback to system detection
if [ -n "$ARCH" ]; then
  # Use provided architecture
  echo "Using provided architecture: $ARCH"
else
  # Auto-detect architecture
  if [ "$(arch)" == "x86_64" ]; then
    ARCH="amd64"
  else
    ARCH="arm64"
  fi
  echo "Auto-detected architecture: $ARCH"
fi

download_image() {
  image_list=(
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rainbond:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-chaos:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-mq:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rainbond-operator:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-worker:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-api:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-init-probe:${VERSION}"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-monitor:v2.20.0"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/apisix-ingress-controller:v1.8.3"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/apisix:3.9.1-debian-fix"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/local-path-provisioner:v0.0.30"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/minio:RELEASE.2023-10-24T04-42-36Z"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/rbd-db:8.0.19"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/registry:2.6.2"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/alpine:latest"
    "${IMAGE_DOMAIN}/${IMAGE_NAMESPACE}/mysql:8.0.34-bitnami"
  )

  for image in "${image_list[@]}"; do
    docker pull "${image}"
  done

  docker save -o rainbond-offline-images.tar "${image_list[@]}"

  for image in "${image_list[@]}"; do
    docker rmi -f "${image}"
  done
}


download_rke2() {
  wget -O rke2-images-linux.tar https://pkg.rainbond.com/rke2/v1.30.4+rke2r1/rke2-images-linux-$ARCH.tar
  wget -O rke2.linux-$ARCH.tar.gz https://pkg.rainbond.com/rke2/v1.30.4+rke2r1/rke2.linux-$ARCH.tar.gz
  wget -O sha256sum-$ARCH.txt https://pkg.rainbond.com/rke2/v1.30.4+rke2r1/sha256sum-$ARCH.txt
  wget -O rke2-install.sh https://rancher-mirror.rancher.cn/rke2/install.sh
}


download_helm() {
  wget -O helm-linux.tar.gz https://mirrors.huaweicloud.com/helm/v3.18.6/helm-v3.18.6-linux-$ARCH.tar.gz
  tar -zxvf helm-linux.tar.gz
  mv linux-${ARCH}/helm .
  chmod +x helm
  rm -rf linux-${ARCH} helm-linux.tar.gz
}

download_rainbond_chart() {
  ./helm repo add rainbond https://chart.rainbond.com
  ./helm repo update
  ./helm pull rainbond/rainbond
  mv rainbond-*.tgz rainbond.tgz
}

build_roi() {
docker run --rm -v "$(pwd)":/workspace -w /workspace -e GOPROXY=https://goproxy.cn,direct -e GOSUMDB=sum.golang.google.cn \
  registry.cn-hangzhou.aliyuncs.com/zqqq/golang:1.24 \
  sh -c "go mod tidy && go build -o roi cmd/main.go"

}

upload_tos() {
  wget https://m645b3e1bb36e-mrap.mrap.accesspoint.tos-global.volces.com/linux/$ARCH/tosutil
  chmod +x tosutil
  ./tosutil config -i "$TOS_ACCESS_KEY" -k "$TOS_SECRET_KEY" -e tos-cn-beijing.volces.com -re cn-beijing
  ./tosutil cp roi.tar.gz tos://rainbond-pkg/v6.x/rainbond-offline-installer-package-${VERSION%-release}-"$ARCH".tar.gz
}

main() {
  build_roi
  download_image
  download_rke2
  download_helm
  download_rainbond_chart

  offline_package_dir=rainbond-offline-installer-package-${VERSION%-release}

  mkdir "$offline_package_dir"
  mv roi "$offline_package_dir"
  mv rainbond.tgz "$offline_package_dir"
  mv rainbond-offline-images.tar "$offline_package_dir"
  mv rke2-images-linux.tar "$offline_package_dir"
  mv rke2.linux-"$ARCH".tar.gz "$offline_package_dir"
  mv rke2-install.sh "$offline_package_dir"
  mv sha256sum-"$ARCH".txt "$offline_package_dir"
  mv helm "$offline_package_dir"

  tar -zcvf rainbond-offline-installer-package-${VERSION%-release}-"$ARCH".tar.gz "$offline_package_dir"
  upload_tos
}

main