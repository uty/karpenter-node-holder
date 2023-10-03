# karpenter-node-holder
Holds nodes for a set amount of time by annotating them with `karpenter.sh/do-not-consolidate` effectively pausing Karpenter consolidation

This is a temporary solution until [karpenter#3059](https://github.com/aws/karpenter/issues/3059) is released.
