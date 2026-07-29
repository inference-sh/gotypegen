patch:
	@new_tag=$$(svu patch) && \
	git commit --allow-empty -m "chore: bump to $$new_tag" && \
	git tag "$$new_tag" && \
	echo "Tagged $$new_tag"

minor:
	@new_tag=$$(svu minor) && \
	git commit --allow-empty -m "chore: bump to $$new_tag" && \
	git tag "$$new_tag" && \
	echo "Tagged $$new_tag"

major:
	@new_tag=$$(svu major) && \
	git commit --allow-empty -m "chore: bump to $$new_tag" && \
	git tag "$$new_tag" && \
	echo "Tagged $$new_tag"

release:
	@git push origin HEAD $$(git describe --tags --abbrev=0) && \
	echo "Pushed $$(git describe --tags --abbrev=0)"

.PHONY: patch minor major release
