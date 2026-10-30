/*
 * iobench.c - portable file/IO microbenchmarks (XFS reflink aware).
 *
 * Build: gcc -O2 -o iobench iobench.c -lm
 * Usage: iobench <clone|clone_chain|fsync|pcache|odirect|seq|fallocate> --dir DIR
 *
 * Output: RESULT,<test>,<variant>,<size_bytes>,<threads>,<value>,<unit>
 */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/fs.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

static const char *g_dir = "/tmp";
static double now_sec(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec * 1e-9;
}
static void emit(const char *test, const char *variant, unsigned long long size,
                 int threads, double value, const char *unit) {
    printf("RESULT,%s,%s,%llu,%d,%.6f,%s\n", test, variant, size, threads, value, unit);
    fflush(stdout);
}
static uint64_t rs = 1234567891234567ULL;
static inline uint64_t xr(void) {
    uint64_t x = rs; x ^= x << 13; x ^= x >> 7; x ^= x << 17; rs = x; return x;
}
static int cmp_d(const void *a, const void *b) {
    double x = *(const double *)a, y = *(const double *)b;
    return x < y ? -1 : (x > y ? 1 : 0);
}
static double median(double *v, int n) {
    qsort(v, n, sizeof(double), cmp_d);
    return (n & 1) ? v[n / 2] : 0.5 * (v[n / 2 - 1] + v[n / 2]);
}
static char *path_of(const char *name) {
    static char buf[512];
    snprintf(buf, sizeof(buf), "%s/%s", g_dir, name);
    return buf;
}
static void *abuf(size_t n) {
    void *p = NULL;
    if (posix_memalign(&p, 4096, n) != 0) { fprintf(stderr, "alloc fail\n"); exit(1); }
    return p;
}
static int make_file(const char *path, size_t size) {
    int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror("open"); return -1; }
    char *b = abuf(1 << 20);
    for (size_t i = 0; i < (1 << 20); i++) b[i] = (char)(i * 7);
    for (size_t o = 0; o < size; o += 1 << 20) {
        ssize_t w = pwrite(fd, b, 1 << 20, (off_t)o);
        if (w != (1 << 20)) { perror("pwrite"); break; }
    }
    free(b);
    fsync(fd);
    return fd;
}

static void test_clone(void) {
    size_t sizes[] = {10ULL << 20, 100ULL << 20, 1024ULL << 20};
    for (int s = 0; s < 3; s++) {
        size_t size = sizes[s];
        char *src = strdup(path_of("clone-src.bin"));
        int sfd = make_file(src, size);
        if (sfd < 0) return;
        close(sfd);
        double samples[7];
        for (int r = 0; r < 7; r++) {
            char *dst = path_of("clone-dst.bin");
            unlink(dst);
            int dfd = open(dst, O_WRONLY | O_CREAT | O_TRUNC, 0644);
            int sfd2 = open(src, O_RDONLY);
            double t0 = now_sec();
            int rc = ioctl(dfd, FICLONE, sfd2);
            double t1 = now_sec();
            if (rc != 0) { perror("FICLONE"); }
            close(sfd2); close(dfd);
            samples[r] = (t1 - t0) * 1e3;
            unlink(dst);
        }
        char var[64];
        snprintf(var, sizeof(var), "ficlone_%zuMB", size >> 20);
        emit("io", var, size, 1, median(samples, 7), "ms");
        emit("io", "ficlone_us_per_MB", size, 1,
             median(samples, 7) * 1000.0 / (double)(size >> 20), "us/MB");
        unlink(src);
        free(src);
    }
}

static void test_clone_frag(void) {
    /* 16K random 4K writes spread over 1GB -> thousands of extents */
    size_t size = 1024ULL << 20;
    char *p = strdup(path_of("frag.bin"));
    int fd = open(p, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror("open"); return; }
    ftruncate(fd, size);
    char *b = abuf(4096);
    memset(b, 0x5a, 4096);
    for (int i = 0; i < 16384; i++) {
        off_t off = (off_t)(xr() % (size / 4096)) * 4096;
        pwrite(fd, b, 4096, off);
    }
    fsync(fd);
    close(fd);
    double samples[7];
    for (int r = 0; r < 7; r++) {
        char *dst = path_of("frag-dst.bin");
        unlink(dst);
        int dfd = open(dst, O_WRONLY | O_CREAT | O_TRUNC, 0644);
        int sfd = open(p, O_RDONLY);
        double t0 = now_sec();
        int rc = ioctl(dfd, FICLONE, sfd);
        double t1 = now_sec();
        if (rc != 0) perror("FICLONE frag");
        close(sfd); close(dfd);
        samples[r] = (t1 - t0) * 1e3;
        unlink(dst);
    }
    emit("io", "ficlone_frag_1G", size, 1, median(samples, 7), "ms");
    unlink(p);
    free(p);
    free(b);
}

static void test_clone_chain(void) {
    size_t size = 100ULL << 20;
    char *src = strdup(path_of("chain-src.bin"));
    int sfd = make_file(src, size);
    if (sfd < 0) return;
    close(sfd);
    char names[12][64];
    for (int depth = 1; depth <= 8; depth++) {
        for (int i = 0; i < 12; i++) snprintf(names[i], sizeof(names[i]), "chain-%d-%d.bin", depth, i);
        /* build chain of length depth */
        char prev[64];
        snprintf(prev, sizeof(prev), "%s", src);
        double samples[5];
        for (int r = 0; r < 5; r++) {
            /* cleanup */
            for (int i = 0; i < 12; i++) unlink(path_of(names[i]));
            snprintf(prev, sizeof(prev), "%s", src);
            double t0 = 0, t1 = 0;
            for (int d = 0; d < depth; d++) {
                char *dst = path_of(names[d]);
                unlink(dst);
                int dfd = open(dst, O_WRONLY | O_CREAT | O_TRUNC, 0644);
                int pfd = open(prev, O_RDONLY);
                double a0 = now_sec();
                int rc = ioctl(dfd, FICLONE, pfd);
                double a1 = now_sec();
                if (rc != 0) perror("FICLONE chain");
                close(pfd); close(dfd);
                if (d == depth - 1) { t0 = a0; t1 = a1; }
                snprintf(prev, sizeof(prev), "%s", dst);
            }
            samples[r] = (t1 - t0) * 1e3;
        }
        char var[64];
        snprintf(var, sizeof(var), "ficlone_depth_%d", depth);
        emit("io", var, size, 1, median(samples, 5), "ms");
        for (int i = 0; i < 12; i++) unlink(path_of(names[i]));
    }
    unlink(src);
    free(src);
}

static void test_fsync(void) {
    char *p = path_of("fsync.bin");
    int fd = open(p, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror("open"); return; }
    size_t mb = 1 << 20;
    char *b = abuf(mb);
    memset(b, 0xab, mb);
    double s[101], sd[101];
    for (int r = 0; r < 101; r++) {
        pwrite(fd, b, mb, (off_t)(r % 16) * mb);
        double t0 = now_sec();
        fsync(fd);
        double t1 = now_sec();
        s[r] = (t1 - t0) * 1e6;
        pwrite(fd, b, mb, (off_t)(r % 16) * mb);
        t0 = now_sec();
        fdatasync(fd);
        t1 = now_sec();
        sd[r] = (t1 - t0) * 1e6;
    }
    emit("io", "fsync_1MB", mb, 1, median(s + 1, 100), "us");
    emit("io", "fdatasync_1MB", mb, 1, median(sd + 1, 100), "us");
    close(fd); unlink(p); free(b);
}

static void test_pcache(void) {
    size_t size = 2ULL << 30;
    char *p = path_of("pcache.bin");
    int fd = make_file(p, size);
    if (fd < 0) return;
    /* warm: sequential read */
    size_t chunk = 1 << 20;
    char *b = abuf(chunk);
    double t0 = now_sec();
    for (size_t o = 0; o < size; o += chunk) { ssize_t r = pread(fd, b, chunk, (off_t)o); (void)r; }
    double t1 = now_sec();
    emit("io", "buffered_seqread_1M", size, 1, (double)size / (t1 - t0) / 1e9, "GB/s");

    size_t bslist[] = {4096, 65536, 1 << 20};
    long opslist[] = {40000, 8000, 1000};
    for (int k = 0; k < 3; k++) {
        size_t bs = bslist[k];
        long ops = opslist[k];
        char *buf = abuf(bs);
        double samples[5];
        for (int r = 0; r < 5; r++) {
            volatile long sink = 0;
            double a0 = now_sec();
            for (long i = 0; i < ops; i++) {
                off_t off = (off_t)(xr() % (size / bs)) * (off_t)bs;
                ssize_t n = pread(fd, buf, bs, off);
                sink += n;
            }
            double a1 = now_sec();
            samples[r] = (a1 - a0) * 1e9 / (double)ops;
            (void)sink;
        }
        char var[64];
        snprintf(var, sizeof(var), "pcache_rand_%zuK", bs >> 10);
        double ns = median(samples, 5);
        emit("io", var, bs, 1, ns, "ns");
        emit("io", "pcache_rand_GBs", bs, 1, (double)bs / ns, "GB/s");
        free(buf);
    }
    close(fd); unlink(p); free(b);
}

static void test_odirect(void) {
    size_t size = 2ULL << 30;
    char *p = path_of("odirect.bin");
    int fd0 = make_file(p, size);
    if (fd0 < 0) return;
    close(fd0);
    int fd = open(p, O_RDONLY | O_DIRECT);
    if (fd < 0) { perror("O_DIRECT open"); unlink(p); return; }
    size_t bslist[] = {4096, 65536, 1 << 20};
    long opslist[] = {40000, 8000, 1000};
    for (int k = 0; k < 3; k++) {
        size_t bs = bslist[k];
        long ops = opslist[k];
        char *buf = abuf(bs);
        double samples[5];
        for (int r = 0; r < 5; r++) {
            volatile long sink = 0;
            double a0 = now_sec();
            for (long i = 0; i < ops; i++) {
                off_t off = (off_t)(xr() % (size / bs)) * (off_t)bs;
                ssize_t n = pread(fd, buf, bs, off);
                sink += n;
            }
            double a1 = now_sec();
            samples[r] = (a1 - a0) * 1e9 / (double)ops;
            (void)sink;
        }
        char var[64];
        snprintf(var, sizeof(var), "odirect_rand_%zuK", bs >> 10);
        double ns = median(samples, 5);
        emit("io", var, bs, 1, ns, "ns");
        emit("io", "odirect_rand_MBs", bs, 1, (double)bs / ns, "MB/s");
        emit("io", "odirect_rand_IOPS", bs, 1, 1e9 / ns, "IOPS");
        free(buf);
    }
    /* sequential O_DIRECT 1M */
    size_t chunk = 1 << 20;
    char *b = abuf(chunk);
    double a0 = now_sec();
    for (size_t o = 0; o < size; o += chunk) { ssize_t r = pread(fd, b, chunk, (off_t)o); (void)r; }
    double a1 = now_sec();
    emit("io", "odirect_seqread_1M", size, 1, (double)size / (a1 - a0) / 1e9, "GB/s");
    free(b);
    close(fd); unlink(p);
}

static void test_fallocate(void) {
    char *p = path_of("falloc.bin");
    int fd = open(p, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror("open"); return; }
    size_t size = 1ULL << 30;
    double samples[7];
    for (int r = 0; r < 7; r++) {
        ftruncate(fd, 0);
        ftruncate(fd, size);
        double t0 = now_sec();
        int rc = fallocate(fd, FALLOC_FL_ZERO_RANGE, 0, size);
        double t1 = now_sec();
        if (rc != 0) perror("fallocate");
        samples[r] = (t1 - t0) * 1e3;
    }
    emit("io", "fallocate_zero_1G", size, 1, median(samples, 7), "ms");
    close(fd); unlink(p);
}

int main(int argc, char **argv) {
    const char *mode = (argc > 1) ? argv[1] : "pcache";
    for (int i = 2; i < argc; i++)
        if (strcmp(argv[i], "--dir") == 0 && i + 1 < argc) g_dir = argv[++i];
    if (strcmp(mode, "clone") == 0) test_clone();
    else if (strcmp(mode, "clone_frag") == 0) test_clone_frag();
    else if (strcmp(mode, "clone_chain") == 0) test_clone_chain();
    else if (strcmp(mode, "fsync") == 0) test_fsync();
    else if (strcmp(mode, "pcache") == 0) test_pcache();
    else if (strcmp(mode, "odirect") == 0) test_odirect();
    else if (strcmp(mode, "fallocate") == 0) test_fallocate();
    else { fprintf(stderr, "unknown mode\n"); return 2; }
    return 0;
}
