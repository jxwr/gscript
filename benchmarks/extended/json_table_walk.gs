// Extended benchmark: JSON-like table construction and nested traversal.

func make_document(i) {
    kind := "article"
    if i % 5 == 0 {
        kind = "invoice"
    } elseif i % 7 == 0 {
        kind = "event"
    }

    return {
        id: i,
        kind: kind,
        active: i % 4 != 0,
        user: {id: i % 503, tier: i % 6, region: (i % 9) + 1},
        metrics: {views: i * 3 % 1000, clicks: i * 7 % 251, errors: i % 13},
        tags: {first: string.format("tag%d", i % 11), second: string.format("team%d", i % 5), third: string.format("r%d", i % 9)}
    }
}

func build_documents(n) {
    docs := {}
    for i := 1; i <= n; i++ {
        docs[i] = make_document(i)
    }
    return docs
}

func walk_documents(docs, n, passes) {
    checksum := 0
    for pass := 1; pass <= passes; pass++ {
        for i := 1; i <= n; i++ {
            doc := docs[i]
            metrics := doc.metrics
            user := doc.user
            tags := doc.tags
            tag := tags.first
            tag_choice := (i + pass) % 3
            if tag_choice == 1 {
                tag = tags.second
            } elseif tag_choice == 2 {
                tag = tags.third
            }

            if doc.active {
                score := metrics.views + metrics.clicks * 3 - metrics.errors * 5 + user.tier + #doc.kind + #tag
                checksum = (checksum + score + user.region) % 1000000007
                if pass % 3 == 0 {
                    metrics.views = (metrics.views + user.tier + i) % 2000
                }
            } else {
                checksum = (checksum + doc.id + metrics.errors + #tags.first) % 1000000007
            }
        }
    }
    return checksum
}

N := 18000
PASSES := 80

t0 := time.now()
docs := build_documents(N)
checksum := walk_documents(docs, N, PASSES)
elapsed := time.since(t0)

print(string.format("json_table_walk documents=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
