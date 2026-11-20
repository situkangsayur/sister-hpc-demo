# Jenkins LTS + plugin yang dipin + klien buildctl (BuildKit). Tidak ada Docker CLI atau socket:
# image dibangun oleh service buildkitd (rootless) yang dihubungi lewat TCP.
FROM moby/buildkit:v0.34.2-rootless AS buildkit

FROM jenkins/jenkins:2.580.2-lts-jdk21
COPY --from=buildkit /usr/bin/buildctl /usr/local/bin/buildctl
COPY plugins.txt /usr/share/jenkins/ref/plugins.txt
RUN jenkins-plugin-cli --plugin-file /usr/share/jenkins/ref/plugins.txt
COPY casc.yaml /usr/share/jenkins/casc/casc.yaml
# runSetupWizard=false: tanpa wizard klik; seluruh konfigurasi dari casc.yaml (JCasC).
# NOTIFY_COMMIT_ACCESS_CONTROL=disabled-for-polling: webhook /git/notifyCommit dari Gitea boleh
# memicu polling tanpa token (hanya polling, tidak bisa memaksa build dengan parameter).
ENV CASC_JENKINS_CONFIG=/usr/share/jenkins/casc/casc.yaml \
    JAVA_OPTS="-Djenkins.install.runSetupWizard=false -Dhudson.plugins.git.GitStatus.NOTIFY_COMMIT_ACCESS_CONTROL=disabled-for-polling -Xms256m -Xmx640m"
